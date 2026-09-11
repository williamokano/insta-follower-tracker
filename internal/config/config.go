// Package config reads the service's runtime configuration from the
// environment, which is how it is driven inside a container.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved runtime configuration.
type Config struct {
	// DataDir holds the SQLite database and the uploaded exports. Mounting it
	// as a volume is what makes the deployment persistent.
	DataDir string
	// Addr is the listen address.
	Addr string
	// MaxUploadBytes caps a single upload.
	MaxUploadBytes int64
	// RetainUploads keeps raw exports on disk after processing.
	RetainUploads bool
	// LogLevel controls slog verbosity.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish.
	ShutdownTimeout time.Duration
}

// Defaults.
const (
	DefaultDataDir         = "/data"
	DefaultAddr            = ":8080"
	DefaultMaxUploadBytes  = 100 << 20 // 100 MiB
	DefaultShutdownTimeout = 15 * time.Second
)

// Load reads configuration from the environment, applying defaults.
func Load() (Config, error) {
	cfg := Config{
		DataDir:         envString("IFT_DATA_DIR", DefaultDataDir),
		Addr:            envString("IFT_ADDR", DefaultAddr),
		MaxUploadBytes:  DefaultMaxUploadBytes,
		RetainUploads:   true,
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: DefaultShutdownTimeout,
	}

	if raw, ok := os.LookupEnv("IFT_MAX_UPLOAD_BYTES"); ok {
		v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("IFT_MAX_UPLOAD_BYTES must be a positive integer, got %q", raw)
		}
		cfg.MaxUploadBytes = v
	}

	if raw, ok := os.LookupEnv("IFT_RETAIN_UPLOADS"); ok {
		v, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return Config{}, fmt.Errorf("IFT_RETAIN_UPLOADS must be a boolean, got %q", raw)
		}
		cfg.RetainUploads = v
	}

	if raw, ok := os.LookupEnv("IFT_LOG_LEVEL"); ok {
		var level slog.Level
		if err := level.UnmarshalText([]byte(strings.TrimSpace(raw))); err != nil {
			return Config{}, fmt.Errorf("IFT_LOG_LEVEL must be debug, info, warn or error, got %q", raw)
		}
		cfg.LogLevel = level
	}

	if cfg.DataDir == "" {
		return Config{}, fmt.Errorf("IFT_DATA_DIR must not be empty")
	}

	return cfg, nil
}

// DatabasePath is where the SQLite database lives.
func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "tracker.db") }

// UploadDir is where raw uploaded exports are kept.
func (c Config) UploadDir() string { return filepath.Join(c.DataDir, "uploads") }

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}
