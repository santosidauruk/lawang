package queue

import (
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/outbox"
)

type Server struct {
	server                *asynq.Server
	providerSubmitHandler asynq.Handler
	sessionExpiryHandler  asynq.Handler
	storageCleanupHandler asynq.Handler
	logger                *slog.Logger
}

func NewAsynqTaskServer(
	config RedisConfig,
	concurrency int,
	shutdownTimeout time.Duration,
	providerSubmitService ProviderSubmissionTaskService,
	sessionExpiryService SessionExpiryTaskService,
	storageCleanupService StorageCleanupTaskService,
	cleanupBatchSize int32,
	logger *slog.Logger) *Server {
	return &Server{
		server: asynq.NewServer(redisClientOpt(config), asynq.Config{
			Concurrency:     concurrency,
			ShutdownTimeout: shutdownTimeout,
			RetryDelayFunc:  TaskRetryDelay,
			Logger:          safeAsynqLogger{logger: logger},
		}),
		providerSubmitHandler: NewProviderSubmissionHandler(providerSubmitService),
		sessionExpiryHandler:  NewSessionExpiryHandler(sessionExpiryService),
		storageCleanupHandler: NewStorageCleanupHandler(storageCleanupService, cleanupBatchSize),
		logger:                logger,
	}
}
func redisClientOpt(config RedisConfig) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: config.Address, Password: config.Password, DB: config.Database}
}

func (s *Server) Start() error {
	mux := asynq.NewServeMux()
	mux.Handle(outbox.TaskTypeProviderSubmit, safeTaskHandler{
		next:   s.providerSubmitHandler,
		logger: s.logger,
	})
	mux.Handle(TaskTypeSessionExpiry, safeTaskHandler{
		next:   s.sessionExpiryHandler,
		logger: s.logger,
	})
	mux.Handle(TaskTypeStorageCleanup, safeTaskHandler{
		next: s.storageCleanupHandler, logger: s.logger,
	})
	return s.server.Start(mux)
}
func (s *Server) Shutdown() { s.server.Shutdown() }

func ProviderSubmitRetryDelay(retryCount int, _ error, _ *asynq.Task) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	if retryCount > MaxProviderRetries {
		retryCount = MaxProviderRetries
	}
	return time.Second * time.Duration(1<<(retryCount-1))
}

func TaskRetryDelay(n int, err error, task *asynq.Task) time.Duration {
	if task != nil && task.Type() == outbox.TaskTypeProviderSubmit {
		return ProviderSubmitRetryDelay(n, err, task)
	}
	return asynq.DefaultRetryDelayFunc(n, err, task)
}

type safeAsynqLogger struct{ logger *slog.Logger }

func (l safeAsynqLogger) Debug(...interface{}) { l.logger.Debug("queue internal event") }
func (l safeAsynqLogger) Info(...interface{})  { l.logger.Info("queue internal event") }
func (l safeAsynqLogger) Warn(...interface{})  { l.logger.Warn("queue internal event") }
func (l safeAsynqLogger) Error(...interface{}) { l.logger.Error("queue internal event") }
func (l safeAsynqLogger) Fatal(...interface{}) { l.logger.Error("queue fatal event") }
