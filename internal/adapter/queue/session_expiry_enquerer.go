package queue

import (
	"context"

	"github.com/hibiken/asynq"
)

type SessionExpiryEnqueuer struct {
	client *asynq.Client
}

func NewSessionExpiryEnqueuer(redisConfig RedisConfig) *SessionExpiryEnqueuer {
	return &SessionExpiryEnqueuer{
		client: asynq.NewClient(redisClientOpt(redisConfig)),
	}
}

func (s *SessionExpiryEnqueuer) EnqueueImmediate(ctx context.Context) error {
	_, err := s.client.EnqueueContext(ctx, NewSessionExpiryTask())
	if err != nil {
		return err
	}

	return nil
}

func (p *SessionExpiryEnqueuer) Close() error { return p.client.Close() }
