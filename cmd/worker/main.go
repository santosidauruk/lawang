package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/adapter/providerhttp"
	queueadapter "github.com/santosidauruk/lawang-go/internal/adapter/queue"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/platform/config"
	"github.com/santosidauruk/lawang-go/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.LoadWorker()
	if err != nil {
		bootstrapLogger.Error("invalid worker configuration", "error", err)
		return 1
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed")
		return 1
	}
	defer database.Close()

	if err := database.Ping(ctx); err != nil {
		logger.Error("database unavailable")
		return 1
	}

	redisConfig := queueadapter.RedisConfig{
		Address:  cfg.RedisAddress,
		Password: cfg.RedisPassword,
		Database: cfg.RedisDatabase,
	}
	publisher := queueadapter.NewPublisherForRedis(redisConfig)
	defer publisher.Close()

	outboxStore := postgres.NewOutboxStore(database)
	relay := outbox.NewRelay(outboxStore, publisher, systemClock{}, cfg.OutboxClaimLease)

	provider, err := providerhttp.NewClient(cfg.ProviderBaseURL, cfg.ProviderTimeout)
	if err != nil {
		logger.Error("provider configuration failed")
		return 1
	}
	providerReader := postgres.NewProviderSubmissionReader(database)
	taskService, err := providersubmission.NewTaskService(providerReader, provider, cfg.ProviderCallbackURL)
	if err != nil {
		logger.Error("provider task configuration failed")
		return 1
	}

	providerServer := queueadapter.NewProviderServer(redisConfig, cfg.Concurrency, cfg.ShutdownTimeout, taskService, logger)
	if err = serveWorker(ctx, relay, providerServer, cfg.RelayInterval, logger); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	return 0
}

type relayRunner interface {
	RunOnce(context.Context) error
}

type taskServer interface {
	Start() error
	Shutdown()
}

func serveWorker(ctx context.Context, relay relayRunner, server taskServer, interval time.Duration, logger *slog.Logger) error {
	if err := server.Start(); err != nil {
		return err
	}
	defer server.Shutdown()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := relay.RunOnce(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("outbox relay attempt failed")
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

type systemClock struct{}

func (s systemClock) Now() time.Time {
	return time.Now()
}
