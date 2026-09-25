package queue

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

type SessionExpiryScheduler struct {
	scheduler *asynq.Scheduler
}

type SessionExpiryEnqueuer struct {
	client *asynq.Client
}

func NewSessionExpiryScheduler(redisConfig RedisConfig, interval time.Duration, logger *slog.Logger) (*SessionExpiryScheduler, error) {
	s := &SessionExpiryScheduler{
		scheduler: asynq.NewScheduler(
			redisClientOpt(redisConfig),
			&asynq.SchedulerOpts{Logger: safeAsynqLogger{logger: logger}}),
	}
	spec := "@every " + interval.String()
	_, err := s.scheduler.Register(spec, NewSessionExpiryTask())
	if err != nil {
		return nil, err
	}
	return s, nil
}

func NewSessionExpiryEnqueuer(redisConfig RedisConfig) *SessionExpiryEnqueuer {
	return &SessionExpiryEnqueuer{
		client: asynq.NewClient(redisClientOpt(redisConfig)),
	}
}

func (s *SessionExpiryScheduler) Start() error {
	return s.scheduler.Start()
}

func (s *SessionExpiryScheduler) Shutdown() {
	s.scheduler.Shutdown()
}

func (s *SessionExpiryEnqueuer) EnqueueImmediate(ctx context.Context) error {
	_, err := s.client.EnqueueContext(ctx, NewSessionExpiryTask())
	if err != nil {
		return err
	}

	return nil

}

func (p *SessionExpiryEnqueuer) Close() error { return p.client.Close() }
