package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/santosidauruk/lawang-go/internal/platform/config"
)

func TestLoadFakeUsesIndependentSafeDefaults(t *testing.T) {
	setValidFakeProviderEnvironment(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("S3_INTERNAL_ENDPOINT", "")
	t.Setenv("S3_PUBLIC_ENDPOINT", "")
	t.Setenv("FAKE_HTTP_ADDRESS", "")
	t.Setenv("CALLBACK_TIMEOUT", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")
	t.Setenv("LOG_LEVEL", "")

	got, err := config.LoadFake()
	if err != nil {
		t.Fatalf("LoadFake() error = %v", err)
	}
	if got.FakeHttpAddress != ":8081" {
		t.Errorf("FakeHttpAddress = %q, want %q", got.FakeHttpAddress, ":8081")
	}
	if got.CallbackTimeout != 2*time.Second {
		t.Errorf("CallbackTimeout = %s, want %s", got.CallbackTimeout, 2*time.Second)
	}
	if got.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, 10*time.Second)
	}
	if got.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %s, want %s", got.LogLevel, slog.LevelInfo)
	}
	if got.ProviderWebhookSecret != "fake-provider-config-secret" {
		t.Error("ProviderWebhookSecret does not match configured value")
	}
}

func TestLoadFakeParsesTypedValues(t *testing.T) {
	setValidFakeProviderEnvironment(t)
	t.Setenv("FAKE_HTTP_ADDRESS", "127.0.0.1:19081")
	t.Setenv("CALLBACK_TIMEOUT", "750ms")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	got, err := config.LoadFake()
	if err != nil {
		t.Fatalf("LoadFake() error = %v", err)
	}
	if got.FakeHttpAddress != "127.0.0.1:19081" {
		t.Errorf("FakeHttpAddress = %q, want %q", got.FakeHttpAddress, "127.0.0.1:19081")
	}
	if got.CallbackTimeout != 750*time.Millisecond {
		t.Errorf("CallbackTimeout = %s, want %s", got.CallbackTimeout, 750*time.Millisecond)
	}
	if got.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, 3*time.Second)
	}
	if got.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %s, want %s", got.LogLevel, slog.LevelDebug)
	}
}

func TestLoadFakeRejectsInvalidTypedValues(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantError string
	}{
		{name: "malformed callback timeout", key: "CALLBACK_TIMEOUT", value: "later", wantError: "CALLBACK_TIMEOUT must be a positive duration"},
		{name: "zero callback timeout", key: "CALLBACK_TIMEOUT", value: "0s", wantError: "CALLBACK_TIMEOUT must be a positive duration"},
		{name: "negative callback timeout", key: "CALLBACK_TIMEOUT", value: "-1s", wantError: "CALLBACK_TIMEOUT must be a positive duration"},
		{name: "malformed shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "later", wantError: "SHUTDOWN_TIMEOUT must be a positive duration"},
		{name: "zero shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "0s", wantError: "SHUTDOWN_TIMEOUT must be a positive duration"},
		{name: "negative shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "-1s", wantError: "SHUTDOWN_TIMEOUT must be a positive duration"},
		{name: "invalid log level", key: "LOG_LEVEL", value: "verbose", wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidFakeProviderEnvironment(t)
			t.Setenv("CALLBACK_TIMEOUT", "2s")
			t.Setenv("SHUTDOWN_TIMEOUT", "10s")
			t.Setenv("LOG_LEVEL", "info")
			t.Setenv(tt.key, tt.value)

			_, err := config.LoadFake()
			if err == nil {
				t.Fatal("LoadFake() error = nil, want validation error")
			}
			if err.Error() != tt.wantError {
				t.Fatalf("LoadFake() error = %q, want %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestLoadFakeRejectsInvalidHTTPAddress(t *testing.T) {
	for _, value := range []string{
		"localhost",
		"http://localhost:8081",
		"localhost:not-a-port",
		"localhost:70000",
	} {
		t.Run(value, func(t *testing.T) {
			setValidFakeProviderEnvironment(t)
			t.Setenv("FAKE_HTTP_ADDRESS", value)

			if _, err := config.LoadFake(); err == nil {
				t.Fatalf("LoadFake() error = nil for FAKE_HTTP_ADDRESS %q", value)
			}
		})
	}
}

func TestLoadFakeRequiresProviderWebhookSecret(t *testing.T) {
	setValidFakeProviderEnvironment(t)
	t.Setenv("PROVIDER_WEBHOOK_SECRET", "")

	_, err := config.LoadFake()
	if err == nil {
		t.Fatal("LoadFake() error = nil, want required PROVIDER_WEBHOOK_SECRET error")
	}
	if got, want := err.Error(), "PROVIDER_WEBHOOK_SECRET is required"; got != want {
		t.Fatalf("LoadFake() error = %q, want %q", got, want)
	}
}

func setValidFakeProviderEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("PROVIDER_WEBHOOK_SECRET", "fake-provider-config-secret")
}
