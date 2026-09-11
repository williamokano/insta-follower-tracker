package config_test

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/williamokano/insta-follower-tracker/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.DataDir != config.DefaultDataDir {
		t.Fatalf("data dir = %q, want %q", cfg.DataDir, config.DefaultDataDir)
	}
	if cfg.Addr != config.DefaultAddr {
		t.Fatalf("addr = %q, want %q", cfg.Addr, config.DefaultAddr)
	}
	if !cfg.RetainUploads {
		t.Fatal("uploads should be retained by default")
	}
	if cfg.DatabasePath() != filepath.Join(config.DefaultDataDir, "tracker.db") {
		t.Fatalf("database path = %q", cfg.DatabasePath())
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("IFT_DATA_DIR", "/config")
	t.Setenv("IFT_ADDR", "127.0.0.1:9000")
	t.Setenv("IFT_MAX_UPLOAD_BYTES", "2048")
	t.Setenv("IFT_RETAIN_UPLOADS", "false")
	t.Setenv("IFT_LOG_LEVEL", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.DataDir != "/config" {
		t.Fatalf("data dir = %q", cfg.DataDir)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Fatalf("addr = %q", cfg.Addr)
	}
	if cfg.MaxUploadBytes != 2048 {
		t.Fatalf("max upload = %d", cfg.MaxUploadBytes)
	}
	if cfg.RetainUploads {
		t.Fatal("retain uploads should be false")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("log level = %v", cfg.LogLevel)
	}
	if cfg.UploadDir() != filepath.Join("/config", "uploads") {
		t.Fatalf("upload dir = %q", cfg.UploadDir())
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string][2]string{
		"negative size":  {"IFT_MAX_UPLOAD_BYTES", "-1"},
		"non numeric":    {"IFT_MAX_UPLOAD_BYTES", "plenty"},
		"bad boolean":    {"IFT_RETAIN_UPLOADS", "sometimes"},
		"unknown level":  {"IFT_LOG_LEVEL", "chatty"},
		"zero byte size": {"IFT_MAX_UPLOAD_BYTES", "0"},
	}

	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			if _, err := config.Load(); err == nil {
				t.Fatalf("expected %s=%s to be rejected", kv[0], kv[1])
			}
		})
	}
}
