package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config contains values validated once at process startup.
type Config struct {
	DatabaseURL     string
	HTTPAddress     string
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

// Load reads and validates process configuration from the environment.
func Load() (Config, error) {
	databaseURL, ok := os.LookupEnv("DATABASE_URL")
	if !ok || databaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	shutdownTimeout, err := positiveDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	logLevel, err := parseLogLevel()
	if err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL:     databaseURL,
		HTTPAddress:     lookupOrDefault("HTTP_ADDRESS", ":8080"),
		ShutdownTimeout: shutdownTimeout,
		LogLevel:        logLevel,
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
