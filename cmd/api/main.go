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
	"github.com/google/uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/personaldetails"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/providerverdict"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/platform/config"
	"github.com/santosidauruk/lawang/internal/platform/httpserver"
	"github.com/santosidauruk/lawang/internal/platform/logging"
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

	clock := systemClock{}
	expiryTransactions := postgresadapter.NewExpiryTransactions(database)
	expiryService := expiry.NewService(expiryTransactions, clock)
	expiryOperation := func(ctx context.Context, sessionID uuid.UUID) error {
		input := expiry.Input{
			SessionID: sessionID,
		}
		if _, err := expiryService.Expire(ctx, input); err != nil {
			return err
		}
		return nil
	}

	store := postgresadapter.NewSessionStore(database)
	tokens := session.NewProductionCryptoTokens()
	sessions := session.NewService(store, tokens, clock, expiryOperation)
	details := personaldetails.NewService(postgresadapter.NewPersonalDetailsTransactions(database), tokens, clock, expiryOperation)

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

	acquireFunc := newAcquireFunc(database)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(acquireFunc)
	extractor := deterministicextractor.New(nil, nil)
	artifactConfirm := artifact.NewService(artifactStore, confirmCoordinator, objectStorage, extractor, tokens, clock, expiryOperation)

	uploadIntentService := artifact.NewUploadIntentService(artifactStore, artifactStore, objectStorage, tokens, clock, expiryOperation)

	verdictTransactor := postgresadapter.NewProviderVerdictTransactions(database)
	providerVerdictService := providerverdict.NewProviderVerdictService(clock, verdictTransactor)
	verifiedBody := &httpapi.VerifiedBody{
		Service:               providerVerdictService,
		ProviderWebhookSecret: cfg.ProviderWebhookSecret,
	}

	submissionTransactor := postgresadapter.NewProviderSubmissionTransactions(database)
	providerSubmissionService := providersubmission.NewService(submissionTransactor, tokens, clock, expiryOperation)
	handler := httpapi.WithRequestLogging(httpapi.NewHandler(sessions, details, artifactConfirm, uploadIntentService, verifiedBody, providerSubmissionService), logger)
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

type pgxConfirmConnLease struct {
	*pgxpool.Conn
}

func (l *pgxConfirmConnLease) Discard(ctx context.Context) error {
	conn := l.Hijack()
	return conn.Close(ctx)
}

func newAcquireFunc(pool *pgxpool.Pool) postgresadapter.AcquireFunc {
	return func(ctx context.Context) (postgresadapter.ConnLease, error) {
		conn, err := pool.Acquire(ctx)

		if err != nil {
			return nil, err
		}

		return &pgxConfirmConnLease{
			Conn: conn,
		}, nil
	}
}
