package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/platform/config"
	"github.com/santosidauruk/lawang-go/internal/platform/httpserver"
	"github.com/santosidauruk/lawang-go/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
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

	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		logger.Error("HTTP listener failed", "error", err)
		return 1
	}

	store := postgresadapter.NewSessionStore(database)
	tokens := session.NewProductionCryptoTokens()
	clock := systemClock{}
	sessions := session.NewService(store, tokens, clock)
	details := personaldetails.NewService(postgresadapter.NewPersonalDetailsTransactions(database), tokens, clock)

	artifactStore := postgresadapter.NewArtifactTransactions(database)

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx, awsconfig.WithRegion(cfg.S3Region), awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.S3AccessKey,
				cfg.S3SecretKey,
				"",
			),
		))
	if err != nil {
		logger.Error("load AWS configuration", "error", err)
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
	if err := objectStorage.EnsureBucket(ctx); err != nil {
		logger.Error("object storage unavailable", "error", err)
		return 1
	}

	extractor := deterministicextractor.New(nil, nil)
	artifactConfirm := artifact.NewService(artifactStore, artifactStore, objectStorage, extractor, tokens, clock)

	uploadIntentService := artifact.NewUploadIntentService(artifactStore, artifactStore, objectStorage, tokens, clock)
	handler := httpapi.WithRequestLogging(httpapi.NewHandler(sessions, details, artifactConfirm, uploadIntentService), logger)
	server := httpserver.New(cfg.HTTPAddress, handler)
	logger.Info("API listening", "address", listener.Addr().String())
	if err := httpserver.Run(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		logger.Error("API stopped with error", "error", err)
		return 1
	}
	logger.Info("API stopped")
	return 0
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
