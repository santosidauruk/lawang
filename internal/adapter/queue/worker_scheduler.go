package queue

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

type WorkerScheduler struct {
	scheduler *asynq.Scheduler
}

func NewWorkerScheduler(redisConfig RedisConfig, expiryInterval time.Duration, cleanupInterval time.Duration, logger *slog.Logger) (*WorkerScheduler, error) {
	if expiryInterval <= 0 || cleanupInterval <= 0 {
		return nil, errors.New("worker scheduler intervals must be positive")
	}

	scheduler := asynq.NewScheduler(
		redisClientOpt(redisConfig),
		&asynq.SchedulerOpts{
			Logger: safeAsynqLogger{logger: logger},
			PostEnqueueFunc: func(_ *asynq.TaskInfo, err error) {
				if err != nil {
					logger.Warn("queue scheduler enqueue failed")
				}
			},
		},
	)

	spec := "@every " + expiryInterval.String()
	_, err := scheduler.Register(spec, NewSessionExpiryTask())
	if err != nil {
		return nil, fmt.Errorf("register session expiry schedule: %w", err)
	}

	if _, err := scheduler.Register(
		"@every "+cleanupInterval.String(),
		NewStorageCleanupTask(),
	); err != nil {
		return nil, fmt.Errorf("register storage cleanup schedule: %w", err)
	}
	return &WorkerScheduler{scheduler: scheduler}, nil
}

func (s *WorkerScheduler) Start() error {
	return s.scheduler.Start()
}

func (s *WorkerScheduler) Shutdown() {
	s.scheduler.Shutdown()
}
