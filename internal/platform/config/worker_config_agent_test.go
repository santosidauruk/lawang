package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/santosidauruk/lawang-go/internal/platform/config"
)

func setValidWorkerEnvironment(t *testing.T) {
	t.Helper()

	values := map[string]string{
		"DATABASE_URL":            "postgres://worker:database-secret@database.internal/lawang",
		"REDIS_ADDRESS":           "redis.internal:6379",
		"REDIS_PASSWORD":          "redis-secret",
		"REDIS_DATABASE":          "2",
		"PROVIDER_BASE_URL":       "https://provider.internal/submissions",
		"PROVIDER_CALLBACK_URL":   "https://lawang.internal/provider/callback",
		"PROVIDER_TIMEOUT":        "3s",
		"PROVIDER_WEBHOOK_SECRET": "webhook-secret",
		"WORKER_CONCURRENCY":      "7",
		"RELAY_INTERVAL":          "125ms",
		"OUTBOX_CLAIM_LEASE":      "45s",
		"SHUTDOWN_TIMEOUT":        "11s",
		"LOG_LEVEL":               "warn",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestLoadWorkerParsesTypedConfiguration(t *testing.T) {
	setValidWorkerEnvironment(t)

	got, err := config.LoadWorker()
	if err != nil {
		t.Fatalf("load worker config: %v", err)
	}
	if got.RedisDatabase != 2 || got.Concurrency != 7 {
		t.Fatalf("unexpected integer config: database=%d concurrency=%d", got.RedisDatabase, got.Concurrency)
	}
	if got.ProviderTimeout != 3*time.Second || got.RelayInterval != 125*time.Millisecond || got.OutboxClaimLease != 45*time.Second || got.ShutdownTimeout != 11*time.Second {
		t.Fatalf("unexpected duration config: %+v", got)
	}
}

func TestLoadWorkerRejectsMissingAndMalformedValuesSafely(t *testing.T) {
	testCases := []struct {
		name      string
		key       string
		value     string
		errorText string
	}{
		{name: "missing database URL", key: "DATABASE_URL", value: "", errorText: "DATABASE_URL is required"},
		{name: "invalid Redis address", key: "REDIS_ADDRESS", value: "redis.internal", errorText: "REDIS_ADDRESS must be a host:port"},
		{name: "negative Redis database", key: "REDIS_DATABASE", value: "-1", errorText: "REDIS_DATABASE must be a non-negative integer"},
		{name: "invalid provider URL", key: "PROVIDER_BASE_URL", value: "provider-secret-without-scheme", errorText: "PROVIDER_BASE_URL must be an absolute HTTP or HTTPS URL"},
		{name: "invalid callback URL", key: "PROVIDER_CALLBACK_URL", value: "ftp://callback-secret", errorText: "PROVIDER_CALLBACK_URL must be an absolute HTTP or HTTPS URL"},
		{name: "missing webhook secret", key: "PROVIDER_WEBHOOK_SECRET", value: "", errorText: "PROVIDER_WEBHOOK_SECRET is required"},
		{name: "invalid provider timeout", key: "PROVIDER_TIMEOUT", value: "0s", errorText: "PROVIDER_TIMEOUT must be a positive duration"},
		{name: "invalid concurrency", key: "WORKER_CONCURRENCY", value: "0", errorText: "WORKER_CONCURRENCY must be a positive integer"},
		{name: "invalid relay interval", key: "RELAY_INTERVAL", value: "bad-secret-duration", errorText: "RELAY_INTERVAL must be a positive duration"},
		{name: "invalid claim lease", key: "OUTBOX_CLAIM_LEASE", value: "-1s", errorText: "OUTBOX_CLAIM_LEASE must be a positive duration"},
		{name: "invalid shutdown timeout", key: "SHUTDOWN_TIMEOUT", value: "0s", errorText: "SHUTDOWN_TIMEOUT must be a positive duration"},
		{name: "invalid log level", key: "LOG_LEVEL", value: "secret-level", errorText: "LOG_LEVEL must be one of"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			setValidWorkerEnvironment(t)
			t.Setenv(testCase.key, testCase.value)

			_, err := config.LoadWorker()
			if err == nil || !strings.Contains(err.Error(), testCase.errorText) {
				t.Fatalf("expected error containing %q, got %v", testCase.errorText, err)
			}
			for _, secret := range []string{"database-secret", "redis-secret", "webhook-secret", testCase.value} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("configuration error leaked value %q: %v", secret, err)
				}
			}
		})
	}
}
