// Command ift runs the Instagram follower tracker service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/williamokano/insta-follower-tracker/internal/config"
	"github.com/williamokano/insta-follower-tracker/internal/httpapi"
	"github.com/williamokano/insta-follower-tracker/internal/store"
	"github.com/williamokano/insta-follower-tracker/internal/tracker"
)

// version is overridden at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Println(version)
			return
		}
	}

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ift:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}

	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing the database failed", "error", err)
		}
	}()

	svc, err := tracker.New(st, tracker.Options{
		UploadDir:      cfg.UploadDir(),
		MaxUploadBytes: cfg.MaxUploadBytes,
		RetainUploads:  cfg.RetainUploads,
		Logger:         log,
	})
	if err != nil {
		return err
	}

	server, err := httpapi.New(svc, httpapi.Options{
		MaxUploadBytes: cfg.MaxUploadBytes,
		Version:        version,
		Logger:         log,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The worker drains the upload queue for as long as the process runs.
	workerDone := make(chan error, 1)
	go func() { workerDone <- svc.Run(ctx) }()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No write timeout: uploads of large exports are legitimately slow, and
		// the body size is already capped.
		IdleTimeout: 60 * time.Second,
	}

	serverDone := make(chan error, 1)
	go func() {
		log.Info("listening",
			"addr", cfg.Addr, "version", version, "data_dir", cfg.DataDir)
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverDone <- err
	}()

	select {
	case err := <-serverDone:
		stop()
		<-workerDone
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-workerDone; err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	return <-serverDone
}
