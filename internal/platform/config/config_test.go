package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/santosidauruk/lawang-go/internal/platform/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want required DATABASE_URL error")
	}
	if got, want := err.Error(), "DATABASE_URL is required"; got != want {
		t.Fatalf("Load() error = %q, want %q", got, want)
	}
}

func TestLoadRejectsMalformedValuesWithoutExposingSecrets(t *testing.T) {
	const databaseURL = "postgresql://lawang:do-not-log-this@localhost:5432/lawang_db_go"
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want malformed duration error")
	}
	if got, want := err.Error(), "SHUTDOWN_TIMEOUT must be a positive duration"; got != want {
		t.Fatalf("Load() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), databaseURL) {
		t.Fatal("Load() error exposes DATABASE_URL")
	}
}

func TestLoadUsesSafeLocalDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("LOG_LEVEL", "")

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddress != ":8080" {
		t.Errorf("HTTPAddress = %q, want %q", got.HTTPAddress, ":8080")
	}
	if got.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, 10*time.Second)
	}
	if got.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %s, want %s", got.LogLevel, slog.LevelInfo)
	}
}

func TestLoadParsesTypedValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddress != "127.0.0.1:9090" {
		t.Errorf("HTTPAddress = %q, want %q", got.HTTPAddress, "127.0.0.1:9090")
	}
	if got.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, 3*time.Second)
	}
	if got.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %s, want %s", got.LogLevel, slog.LevelDebug)
	}
}
