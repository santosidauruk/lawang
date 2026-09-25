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
	logger                *slog.Logger
}

func NewAsynqTaskServer(
	config RedisConfig,
	concurrency int,
	shutdownTimeout time.Duration,
	providerSubmitService ProviderSubmissionTaskService,
	sessionExpiryService SessionExpiryTaskService,
	logger *slog.Logger) *Server {
	return &Server{
		server: asynq.NewServer(redisClientOpt(config), asynq.Config{
			Concurrency:     concurrency,
			ShutdownTimeout: shutdownTimeout,
			RetryDelayFunc:  ProviderRetryDelay,
			Logger:          safeAsynqLogger{logger: logger},
		}),
		providerSubmitHandler: NewProviderSubmissionHandler(providerSubmitService),
		sessionExpiryHandler:  NewSessionExpiryHandler(sessionExpiryService),
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
	return s.server.Start(mux)
}
func (s *Server) Shutdown() { s.server.Shutdown() }

func ProviderRetryDelay(retryCount int, _ error, _ *asynq.Task) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	if retryCount > MaxProviderRetries {
		retryCount = MaxProviderRetries
	}
	return time.Second * time.Duration(1<<(retryCount-1))
}

type safeAsynqLogger struct{ logger *slog.Logger }

func (l safeAsynqLogger) Debug(...interface{}) { l.logger.Debug("queue internal event") }
func (l safeAsynqLogger) Info(...interface{})  { l.logger.Info("queue internal event") }
func (l safeAsynqLogger) Warn(...interface{})  { l.logger.Warn("queue internal event") }
func (l safeAsynqLogger) Error(...interface{}) { l.logger.Error("queue internal event") }
func (l safeAsynqLogger) Fatal(...interface{}) { l.logger.Error("queue fatal event") }
