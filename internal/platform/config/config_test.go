package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/santosidauruk/lawang/internal/platform/config"
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

	setValidS3Environment(t)

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

	setValidS3Environment(t)

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

	setValidS3Environment(t)

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

func TestLoadParsesS3Configuration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")

	want := map[string]string{
		"S3_INTERNAL_ENDPOINT":    "http://minio:9000",
		"S3_PUBLIC_ENDPOINT":      "https://uploads.example.test",
		"S3_REGION":               "ap-southeast-3",
		"S3_ACCESS_KEY":           "test-minio",
		"S3_SECRET_KEY":           "test-minio-password",
		"S3_BUCKET":               "test-artifacts",
		"S3_USE_PATH_STYLE":       "true",
		"PROVIDER_WEBHOOK_SECRET": "secret",
	}

	for key, value := range want {
		t.Setenv(key, value)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.S3InternalEndpoint != want["S3_INTERNAL_ENDPOINT"] {
		t.Errorf("S3InternalEndpoint = %q, want %q", got.S3InternalEndpoint, want["S3_INTERNAL_ENDPOINT"])
	}
	if got.S3PublicEndpoint != want["S3_PUBLIC_ENDPOINT"] {
		t.Errorf("S3PublicEndpoint = %q, want %q", got.S3PublicEndpoint, want["S3_PUBLIC_ENDPOINT"])
	}
	if got.S3Region != want["S3_REGION"] {
		t.Errorf("S3Region = %q, want %q", got.S3Region, want["S3_REGION"])
	}
	if got.S3AccessKey != want["S3_ACCESS_KEY"] {
		t.Errorf("S3AccessKey = %q, want %q", got.S3AccessKey, want["S3_ACCESS_KEY"])
	}
	if got.S3SecretKey != want["S3_SECRET_KEY"] {
		t.Errorf("S3SecretKey = %q, want %q", got.S3SecretKey, want["S3_SECRET_KEY"])
	}
	if got.S3Bucket != want["S3_BUCKET"] {
		t.Errorf("S3Bucket = %q, want %q", got.S3Bucket, want["S3_BUCKET"])
	}
	if !got.S3UsePathStyle {
		t.Error("S3UsePathStyle = false, want true")
	}
}

func TestLoadParsesFalseS3UsePathStyle(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)
	t.Setenv("S3_USE_PATH_STYLE", "false")

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.S3UsePathStyle {
		t.Error("S3UsePathStyle = true, want false")
	}
}

func TestLoadParsesProviderWebhookSecret(t *testing.T) {
	const secret = "provider-webhook-secret-for-config-test"
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)
	t.Setenv("PROVIDER_WEBHOOK_SECRET", secret)

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProviderWebhookSecret != secret {
		t.Errorf("ProviderWebhookSecret = %q, want configured value", got.ProviderWebhookSecret)
	}
}

func TestLoadRequiresProviderWebhookSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)
	t.Setenv("PROVIDER_WEBHOOK_SECRET", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want required PROVIDER_WEBHOOK_SECRET error")
	}
	if got, want := err.Error(), "PROVIDER_WEBHOOK_SECRET is required"; got != want {
		t.Fatalf("Load() error = %q, want %q", got, want)
	}
}

func TestLoadValidationErrorDoesNotExposeProviderWebhookSecret(t *testing.T) {
	const secret = "do-not-log-provider-webhook-secret"
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)
	t.Setenv("PROVIDER_WEBHOOK_SECRET", secret)
	t.Setenv("S3_PUBLIC_ENDPOINT", "https://[::1")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid endpoint error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("Load() error exposes PROVIDER_WEBHOOK_SECRET")
	}
}

func TestLoadRejectsInvalidS3Endpoints(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"internal without scheme", "S3_INTERNAL_ENDPOINT", "localhost:9000"},
		{"internal opaque value", "S3_INTERNAL_ENDPOINT", "http-not-a-url"},
		{"internal unsupported scheme", "S3_INTERNAL_ENDPOINT", "ftp://localhost:9000"},
		{"internal lookalike scheme", "S3_INTERNAL_ENDPOINT", "httpsx://localhost:9000"},
		{"internal missing host", "S3_INTERNAL_ENDPOINT", "http://"},
		{"internal malformed URL", "S3_INTERNAL_ENDPOINT", "http://[::1"},
		{"public without scheme", "S3_PUBLIC_ENDPOINT", "localhost:9000"},
		{"public opaque value", "S3_PUBLIC_ENDPOINT", "http-not-a-url"},
		{"public unsupported scheme", "S3_PUBLIC_ENDPOINT", "ftp://localhost:9000"},
		{"public lookalike scheme", "S3_PUBLIC_ENDPOINT", "httpsx://localhost:9000"},
		{"public missing host", "S3_PUBLIC_ENDPOINT", "https://"},
		{"public malformed URL", "S3_PUBLIC_ENDPOINT", "https://[::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)

			_, err := config.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want invalid endpoint error")
			}
			want := tt.key + " must be an absolute HTTP or HTTPS URL"
			if got := err.Error(); got != want {
				t.Errorf("Load() error = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadRequiresS3Configuration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)

	tests := []struct {
		key string
	}{
		{"S3_INTERNAL_ENDPOINT"},
		{"S3_PUBLIC_ENDPOINT"},
		{"S3_REGION"},
		{"S3_ACCESS_KEY"},
		{"S3_SECRET_KEY"},
		{"S3_BUCKET"},
		{"S3_USE_PATH_STYLE"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Setenv(tt.key, "")

			_, err := config.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want required configuration error")
			}
			want := tt.key + " is required"
			if got := err.Error(); got != want {
				t.Errorf("Load() error = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadRejectsNonLiteralS3UsePathStyle(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)

	for _, value := range []string{"maybe", "1", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("S3_USE_PATH_STYLE", value)

			_, err := config.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want boolean validation error")
			}
			if got, want := err.Error(), "S3_USE_PATH_STYLE must be true or false"; got != want {
				t.Errorf("Load() error = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadS3ValidationErrorDoesNotExposeSecret(t *testing.T) {
	const secret = "do-not-log-this-s3-secret"
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	setValidS3Environment(t)
	t.Setenv("S3_SECRET_KEY", secret)
	t.Setenv("S3_PUBLIC_ENDPOINT", "https://[::1")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want invalid endpoint error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("Load() error exposes S3_SECRET_KEY")
	}
}

func setValidS3Environment(t *testing.T) {
	t.Helper()

	s3Env := map[string]string{
		"S3_INTERNAL_ENDPOINT":    "http://minio:9000",
		"S3_PUBLIC_ENDPOINT":      "https://uploads.example.test",
		"S3_REGION":               "ap-southeast-3",
		"S3_ACCESS_KEY":           "test-minio",
		"S3_SECRET_KEY":           "test-minio-password",
		"S3_BUCKET":               "test-artifacts",
		"S3_USE_PATH_STYLE":       "true",
		"PROVIDER_WEBHOOK_SECRET": "secret",
	}

	for key, value := range s3Env {
		t.Setenv(key, value)
	}
}
