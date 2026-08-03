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
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	s3Env := map[string]string{
		"S3_INTERNAL_ENDPOINT": "http://localhost:9000",
		"S3_PUBLIC_ENDPOINT":   "http://localhost:9000",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY":        "test-minio",
		"S3_SECRET_KEY":        "test-minio-password",
		"S3_BUCKET":            "test-artifacts",
		"S3_USE_PATH_STYLE":    "true",
	}

	for key, value := range s3Env {
		t.Setenv(key, value)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	if got.S3InternalEndpoint != "http://localhost:9000" {
		t.Errorf("S3 internal endpoint got: %s, want http://localhost:9000", got.S3InternalEndpoint)
	}
	if got.S3PublicEndpoint != "http://localhost:9000" {
		t.Errorf("S3 public endpoint got: %s, want http://localhost:9000", got.S3PublicEndpoint)
	}

	if got.S3Region != "us-east-1" {
		t.Errorf("S3 region got: %s, want us-east-1", got.S3Region)
	}

	if got.S3AccessKey != "test-minio" {
		t.Errorf("S3 access key got: %s, want test-minio", got.S3AccessKey)
	}

	if got.S3SecretKey != "test-minio-password" {
		t.Errorf("S3 secret key got: %s, want test-minio-password", got.S3SecretKey)
	}

	if got.S3Bucket != "test-artifacts" {
		t.Errorf("S3 bucket got: %s, want test-artifacts", got.S3Bucket)
	}

	if !got.S3UsePathStyle {
		t.Errorf("S3 use path style boolean got: %v, want true", got.S3UsePathStyle)
	}

}

func TestPublicEndpointSchemaURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	s3Env := map[string]string{
		"S3_INTERNAL_ENDPOINT": "http://localhost:9000",
		"S3_PUBLIC_ENDPOINT":   "localhost:9000",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY":        "test-minio",
		"S3_SECRET_KEY":        "test-minio-password",
		"S3_BUCKET":            "test-artifacts",
		"S3_USE_PATH_STYLE":    "true",
	}

	for key, value := range s3Env {
		t.Setenv(key, value)
	}

	tests := []struct {
		name         string
		invalidValue string
		expectErr    string
	}{
		{"not a url", "http-not-a-url", "URL must be an absolute HTTP or HTTPS"},
		{"invalid url", "httpwhatever", "URL must be an absolute HTTP or HTTPS"},
		{"wrong scheme", "httpsx://localhost:9000", "URL must be an absolute HTTP or HTTPS"},
		{"no host #1", "http://", "host must not empty"},
		{"no host #2", "https://", "host must not empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("S3_PUBLIC_ENDPOINT", tt.invalidValue)
			_, err := config.Load()
			if err == nil {
				t.Fatalf("expect error, got no error")
			}
			if err.Error() != tt.expectErr || !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("value: %s, expect error or contains error: %s, got %s, ", tt.invalidValue, tt.expectErr, err.Error())
			}
		})
	}
}

func TestInternalEndpointSchemaURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	s3Env := map[string]string{
		"S3_INTERNAL_ENDPOINT": "http://localhost:9000",
		"S3_PUBLIC_ENDPOINT":   "localhost:9000",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY":        "test-minio",
		"S3_SECRET_KEY":        "test-minio-password",
		"S3_BUCKET":            "test-artifacts",
		"S3_USE_PATH_STYLE":    "true",
	}

	for key, value := range s3Env {
		t.Setenv(key, value)
	}

	tests := []struct {
		name         string
		invalidValue string
		expectErr    string
	}{
		{"not a url", "http-not-a-url", "URL must be an absolute HTTP or HTTPS"},
		{"invalid url", "httpwhatever", "URL must be an absolute HTTP or HTTPS"},
		{"wrong scheme", "httpsx://localhost:9000", "URL must be an absolute HTTP or HTTPS"},
		{"no host #1", "http://", "host must not empty"},
		{"no host #2", "https://", "host must not empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("S3_INTERNAL_ENDPOINT", tt.invalidValue)
			_, err := config.Load()
			if err == nil {
				t.Fatalf("expect error, got no error")
			}
			if err.Error() != tt.expectErr || !strings.Contains(err.Error(), tt.expectErr) {
				t.Errorf("expect error or contains error: %s, got %s", tt.expectErr, err.Error())
			}
		})
	}

}

func TestLoadRequiredS3InternalEndpoint(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang")

	_, err := config.Load()
	if err == nil {
		t.Fatalf("got no error, expected error: S3_INTERNAL_ENDPOINT required")
	}
}

func TestAllRequiredKeyConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://lawang:lawang@localhost:5432/lawang_db_go")
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:9090")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("LOG_LEVEL", "debug")

	setValidS3Environment(t)

	tests := []struct {
		name         string
		invalidKey   string
		invalidValue string
		expectErr    string
	}{
		{"missing S3_PUBLIC_ENDPOINT", "S3_PUBLIC_ENDPOINT", "", "S3_PUBLIC_ENDPOINT is required"},
		{"missing S3_REGION", "S3_REGION", "", "S3_REGION is required"},
		{"missing S3_ACCESS_KEY", "S3_ACCESS_KEY", "", "S3_ACCESS_KEY is required"},
		{"missing S3_SECRET_KEY", "S3_SECRET_KEY", "", "S3_SECRET_KEY is required"},
		{"missing S3_BUCKET", "S3_BUCKET", "", "S3_BUCKET is required"},
		{"missing S3_USE_PATH_STYLE", "S3_USE_PATH_STYLE", "", "S3_USE_PATH_STYLE is required"},
		{"missing S3_USE_PATH_STYLE", "S3_USE_PATH_STYLE", "maybe", "S3_USE_PATH_STYLE must be true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.invalidKey, tt.invalidValue)

			_, err := config.Load()
			if err == nil {
				t.Fatalf("expect error: %s, got no error", tt.expectErr)
			}
			if err.Error() != tt.expectErr {
				t.Errorf("got error: %s, want error: %s", err, tt.expectErr)
			}
		})
	}
}

func setValidS3Environment(t *testing.T) {
	t.Helper()

	s3Env := map[string]string{
		"S3_INTERNAL_ENDPOINT": "http://localhost:9000",
		"S3_PUBLIC_ENDPOINT":   "http://localhost:9000",
		"S3_REGION":            "us-east-1",
		"S3_ACCESS_KEY":        "test-minio",
		"S3_SECRET_KEY":        "test-minio-password",
		"S3_BUCKET":            "test-artifacts",
		"S3_USE_PATH_STYLE":    "true",
	}

	for key, value := range s3Env {
		t.Setenv(key, value)
	}
}
