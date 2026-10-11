package main

import (
	"context"
	"io"
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
	"github.com/santosidauruk/lawang/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
	"github.com/santosidauruk/lawang/internal/platform/config"
	"github.com/santosidauruk/lawang/internal/platform/logging"
)

func main() {
	os.Exit(run(context.Background(), os.Stdout, os.Stderr))
}

// run is the testable manual-command boundary for Issue 010 Checkpoint 3. The user
// owns its config loading, dependency construction, bounded context, and cleanup
// service invocation. Until then it fails closed without printing configuration,
// credentials, storage keys, or raw dependency errors.
func run(parentCtx context.Context, stdout io.Writer, stderr io.Writer) int {
	bootstrapLogger := logging.New(stderr, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
		return 1
	}

	logger := logging.New(stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	database, err := pgxpool.New(runCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed")
		return 1
	}
	defer database.Close()
	if err := database.Ping(runCtx); err != nil {
		logger.Error("database unavailable")
		return 1
	}

	clock := systemClock{}

	awsConfig, err := awsconfig.LoadDefaultConfig(
		runCtx, awsconfig.WithRegion(cfg.S3Region), awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.S3AccessKey,
				cfg.S3SecretKey,
				"",
			),
		))
	if err != nil {
		logger.Error("load AWS configuration", "error", "failed to load config")
		return 1
	}

	publicS3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.S3PublicEndpoint)
		options.UsePathStyle = *aws.Bool(cfg.S3UsePathStyle)
	})

	internalS3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(cfg.S3InternalEndpoint)
		options.UsePathStyle = *aws.Bool(cfg.S3UsePathStyle)
	})

	presignClient := s3.NewPresignClient(publicS3Client)
	objectStorage := s3storage.New(presignClient, cfg.S3Bucket, internalS3Client)

	repository := postgres.NewCleanupRepository(database)
	service := cleanup.NewService(repository, objectStorage, clock)

	result, err := service.Run(runCtx, cleanup.Input{Limit: int32(cfg.CleanupBatchSize)})

	if err != nil {
		logger.Error("orphan cleanup failed")
		return 1
	}

	logger.Info("orphan cleanup completed",
		"materialized", result.Materialized,
		"selected", result.Selected,
		"deleted", result.Deleted,
	)
	return 0
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
