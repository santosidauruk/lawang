package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Config contains values validated once at process startup.
type Config struct {
	DatabaseURL           string
	HTTPAddress           string
	ShutdownTimeout       time.Duration
	LogLevel              slog.Level
	S3InternalEndpoint    string
	S3PublicEndpoint      string
	S3Region              string
	S3AccessKey           string
	S3SecretKey           string
	S3Bucket              string
	S3UsePathStyle        bool
	ProviderWebhookSecret string
}

type FakeConfig struct {
	FakeHttpAddress       string
	CallbackTimeout       time.Duration
	ShutdownTimeout       time.Duration
	LogLevel              slog.Level
	ProviderWebhookSecret string
}

type WorkerConfig struct {
	DatabaseURL           string
	RedisAddress          string
	RedisPassword         string
	RedisDatabase         int
	ProviderBaseURL       string
	ProviderCallbackURL   string
	ProviderTimeout       time.Duration
	ProviderWebhookSecret string
	Concurrency           int
	RelayInterval         time.Duration
	OutboxClaimLease      time.Duration
	ShutdownTimeout       time.Duration
	LogLevel              slog.Level
}

// Load reads and validates process configuration from the environment.
func Load() (Config, error) {
	databaseURL, ok := os.LookupEnv("DATABASE_URL")
	if !ok || databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	s3InternalEndpoint, ok := os.LookupEnv("S3_INTERNAL_ENDPOINT")
	if !ok || s3InternalEndpoint == "" {
		return Config{}, errors.New("S3_INTERNAL_ENDPOINT is required")
	}

	if err := validateS3Endpoint("S3_INTERNAL_ENDPOINT", s3InternalEndpoint); err != nil {
		return Config{}, err
	}

	s3PublicEndpoint, ok := os.LookupEnv("S3_PUBLIC_ENDPOINT")
	if !ok || s3PublicEndpoint == "" {
		return Config{}, errors.New("S3_PUBLIC_ENDPOINT is required")
	}

	if err := validateS3Endpoint("S3_PUBLIC_ENDPOINT", s3PublicEndpoint); err != nil {
		return Config{}, err
	}

	s3Region, ok := os.LookupEnv("S3_REGION")
	if !ok || s3Region == "" {
		return Config{}, errors.New("S3_REGION is required")
	}

	s3AccessKey, ok := os.LookupEnv("S3_ACCESS_KEY")
	if !ok || s3AccessKey == "" {
		return Config{}, errors.New("S3_ACCESS_KEY is required")
	}

	s3SecretKey, ok := os.LookupEnv("S3_SECRET_KEY")
	if !ok || s3SecretKey == "" {
		return Config{}, errors.New("S3_SECRET_KEY is required")
	}

	s3Bucket, ok := os.LookupEnv("S3_BUCKET")
	if !ok || s3Bucket == "" {
		return Config{}, errors.New("S3_BUCKET is required")
	}

	s3UsePathStyle, ok := os.LookupEnv("S3_USE_PATH_STYLE")
	if !ok || s3UsePathStyle == "" {
		return Config{}, errors.New("S3_USE_PATH_STYLE is required")
	}

	var s3BoolUsePathStyle bool
	switch s3UsePathStyle {
	case "true":
		s3BoolUsePathStyle = true
	case "false":
		s3BoolUsePathStyle = false
	default:
		return Config{}, errors.New("S3_USE_PATH_STYLE must be true or false")
	}

	shutdownTimeout, err := positiveDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	logLevel, err := parseLogLevel()
	if err != nil {
		return Config{}, err
	}

	providerWebhookSecret, ok := os.LookupEnv("PROVIDER_WEBHOOK_SECRET")
	if !ok || providerWebhookSecret == "" {
		return Config{}, errors.New("PROVIDER_WEBHOOK_SECRET is required")
	}

	return Config{
		DatabaseURL:           databaseURL,
		HTTPAddress:           lookupOrDefault("HTTP_ADDRESS", ":8080"),
		ShutdownTimeout:       shutdownTimeout,
		LogLevel:              logLevel,
		S3InternalEndpoint:    s3InternalEndpoint,
		S3PublicEndpoint:      s3PublicEndpoint,
		S3Region:              s3Region,
		S3AccessKey:           s3AccessKey,
		S3SecretKey:           s3SecretKey,
		S3Bucket:              s3Bucket,
		S3UsePathStyle:        s3BoolUsePathStyle,
		ProviderWebhookSecret: providerWebhookSecret,
	}, nil
}

func LoadFake() (FakeConfig, error) {
	callbackTimeout, err := positiveDuration("CALLBACK_TIMEOUT", 2*time.Second)
	if err != nil {
		return FakeConfig{}, err
	}

	shutdownTimeout, err := positiveDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return FakeConfig{}, err
	}

	logLevel, err := parseLogLevel()
	if err != nil {
		return FakeConfig{}, err
	}

	providerWebhookSecret, ok := os.LookupEnv("PROVIDER_WEBHOOK_SECRET")
	if !ok || providerWebhookSecret == "" {
		return FakeConfig{}, errors.New("PROVIDER_WEBHOOK_SECRET is required")
	}

	fakeHTTPAddress := lookupOrDefault("FAKE_HTTP_ADDRESS", ":8081")
	if err := validateListenAddress("FAKE_HTTP_ADDRESS", fakeHTTPAddress); err != nil {
		return FakeConfig{}, err
	}

	return FakeConfig{
		FakeHttpAddress:       fakeHTTPAddress,
		CallbackTimeout:       callbackTimeout,
		ShutdownTimeout:       shutdownTimeout,
		LogLevel:              logLevel,
		ProviderWebhookSecret: providerWebhookSecret,
	}, nil
}
func lookupOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func positiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback, nil
	}

	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func parseLogLevel() (slog.Level, error) {
	raw, ok := os.LookupEnv("LOG_LEVEL")
	if !ok || raw == "" {
		return slog.LevelInfo, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, errors.New("LOG_LEVEL must be one of debug, info, warn, error")
	}
	return level, nil
}

func validateS3Endpoint(key, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", key)
	}

	return nil
}

func validateListenAddress(key, address string) error {
	_, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf(
			"%s must be a host:port with port between 1 and 65535",
			key,
		)
	}

	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf(
			"%s must be a host:port with port between 1 and 65535",
			key,
		)
	}

	return nil
}

func LoadWorker() (WorkerConfig, error) {
	databaseURL, err := required("DATABASE_URL")
	if err != nil {
		return WorkerConfig{}, err
	}
	redisAddress, err := required("REDIS_ADDRESS")
	if err != nil {
		return WorkerConfig{}, err
	}
	if err := validateListenAddress("REDIS_ADDRESS", redisAddress); err != nil {
		return WorkerConfig{}, err
	}
	providerBaseURL, err := requiredHTTPURL("PROVIDER_BASE_URL")
	if err != nil {
		return WorkerConfig{}, err
	}
	providerCallbackURL, err := requiredHTTPURL("PROVIDER_CALLBACK_URL")
	if err != nil {
		return WorkerConfig{}, err
	}
	providerWebhookSecret, err := required("PROVIDER_WEBHOOK_SECRET")
	if err != nil {
		return WorkerConfig{}, err
	}
	redisDatabase, err := nonNegativeInt("REDIS_DATABASE", 0)
	if err != nil {
		return WorkerConfig{}, err
	}
	concurrency, err := positiveInt("WORKER_CONCURRENCY", 4)
	if err != nil {
		return WorkerConfig{}, err
	}
	providerTimeout, err := positiveDuration("PROVIDER_TIMEOUT", 5*time.Second)
	if err != nil {
		return WorkerConfig{}, err
	}
	relayInterval, err := positiveDuration("RELAY_INTERVAL", 250*time.Millisecond)
	if err != nil {
		return WorkerConfig{}, err
	}
	claimLease, err := positiveDuration("OUTBOX_CLAIM_LEASE", 30*time.Second)
	if err != nil {
		return WorkerConfig{}, err
	}
	shutdownTimeout, err := positiveDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return WorkerConfig{}, err
	}
	logLevel, err := parseLogLevel()
	if err != nil {
		return WorkerConfig{}, err
	}
	return WorkerConfig{
		DatabaseURL: databaseURL, RedisAddress: redisAddress,
		RedisPassword: os.Getenv("REDIS_PASSWORD"), RedisDatabase: redisDatabase,
		ProviderBaseURL: providerBaseURL, ProviderCallbackURL: providerCallbackURL,
		ProviderTimeout: providerTimeout, ProviderWebhookSecret: providerWebhookSecret,
		Concurrency: concurrency, RelayInterval: relayInterval, OutboxClaimLease: claimLease,
		ShutdownTimeout: shutdownTimeout, LogLevel: logLevel,
	}, nil
}

func required(key string) (string, error) {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func requiredHTTPURL(key string) (string, error) {
	value, err := required(key)
	if err != nil {
		return "", err
	}
	parsed, parseErr := url.ParseRequestURI(value)
	if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", key)
	}
	return value, nil
}

func positiveInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func nonNegativeInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}
