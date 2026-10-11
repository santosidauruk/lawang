package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/adapter/providerhttp"
	queueadapter "github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/outbox"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/platform/config"
	"github.com/santosidauruk/lawang/internal/platform/logging"
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

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.S3AccessKey,
				cfg.S3SecretKey,
				"",
			),
		),
	)
	if err != nil {
		logger.Error("storage configuration failed")
		return 1
	}

	s3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.S3InternalEndpoint)
		options.UsePathStyle = cfg.S3UsePathStyle
	})

	clock := systemClock{}
	redisConfig := queueadapter.RedisConfig{
		Address:  cfg.RedisAddress,
		Password: cfg.RedisPassword,
		Database: cfg.RedisDatabase,
	}
	publisher := queueadapter.NewPublisherForRedis(redisConfig)
	defer publisher.Close()

	outboxStore := postgres.NewOutboxStore(database)
	relay := outbox.NewRelay(outboxStore, publisher, clock, cfg.OutboxClaimLease)

	provider, err := providerhttp.NewClient(cfg.ProviderBaseURL, cfg.ProviderTimeout)
	if err != nil {
		logger.Error("provider configuration failed")
		return 1
	}
	providerReader := postgres.NewProviderSubmissionReader(database)
	providerSubmitTaskService, err := providersubmission.NewTaskService(providerReader, provider, cfg.ProviderCallbackURL)
	if err != nil {
		logger.Error("provider task configuration failed")
		return 1
	}

	expiryTransactions := postgres.NewExpiryTransactions(database)
	expiryService := expiry.NewService(expiryTransactions, clock)

	sweepService := expiry.NewSweepService(expiryTransactions, expiryService, clock, cfg.ExpirySweepBatchSize)

	cleanupStorage := s3storage.New(
		s3.NewPresignClient(s3Client),
		cfg.S3Bucket,
		s3Client,
	)
	cleanupRepository := postgres.NewCleanupRepository(database)
	cleanupService := cleanup.NewService(cleanupRepository, cleanupStorage, clock)
	providerServer := queueadapter.NewAsynqTaskServer(redisConfig, cfg.Concurrency, cfg.ShutdownTimeout, providerSubmitTaskService, sweepService, cleanupService, int32(cfg.CleanupBatchSize), logger)

	scheduler, err := queueadapter.NewWorkerScheduler(redisConfig, cfg.ExpirySweepInterval, cfg.CleanupInterval, logger)
	if err != nil {
		logger.Error("scheduler expiry configuration failed")
		return 1
	}

	expEnqueuer := queueadapter.NewSessionExpiryEnqueuer(redisConfig)
	defer expEnqueuer.Close()

	if err = serveWorker(ctx, relay, providerServer, cfg.RelayInterval, scheduler, expEnqueuer, logger); err != nil {
		logger.Error("worker stopped with error", "error", err)
		return 1
	}

	return 0
}

type relayRunner interface {
	RunOnce(context.Context) error
}

type providerSubmitServer interface {
	Start() error
	Shutdown()
}

type expiryScheduler interface {
	Start() error
	Shutdown()
}

type expiryEnqueuer interface {
	EnqueueImmediate(ctx context.Context) error
	Close() error
}

func serveWorker(ctx context.Context, relay relayRunner, taskServer providerSubmitServer, relayInterval time.Duration, expScheduler expiryScheduler, expEnqueuer expiryEnqueuer, logger *slog.Logger) error {
	if err := taskServer.Start(); err != nil {
		return err
	}
	defer taskServer.Shutdown()

	if err := expEnqueuer.EnqueueImmediate(ctx); err != nil {
		return err
	}

	if err := expScheduler.Start(); err != nil {
		return err
	}

	ticker := time.NewTicker(relayInterval)

	defer ticker.Stop()
	defer expScheduler.Shutdown()

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
