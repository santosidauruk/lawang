package integration_test

import (
	"context"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	queueadapter "github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/outbox"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type routeProviderRecorder chan uuid.UUID

func (r routeProviderRecorder) SendToProvider(_ context.Context, id uuid.UUID) error {
	r <- id
	return nil
}

type routeExpiryRecorder chan struct{}

func (r routeExpiryRecorder) Sweep(context.Context) (expiry.SweepResult, error) {
	r <- struct{}{}
	return expiry.SweepResult{}, nil
}

type routeCleanupRecorder chan cleanup.Input

func (r routeCleanupRecorder) Run(_ context.Context, input cleanup.Input) (cleanup.CleanupResult, error) {
	r <- input
	return cleanup.CleanupResult{}, nil
}

func TestWorkerMuxRoutesProviderExpiryAndCleanupThroughRedis(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	container, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, container)
	redisURL, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get Redis URL: %v", err)
	}
	parsed, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	providers := make(routeProviderRecorder, 1)
	expiries := make(routeExpiryRecorder, 1)
	cleanups := make(routeCleanupRecorder, 1)
	logger := slog.New(slog.NewJSONHandler(&synchronizedLogBuffer{}, nil))
	server := queueadapter.NewAsynqTaskServer(
		queueadapter.RedisConfig{Address: parsed.Host}, 3, time.Second,
		providers, expiries, cleanups, 7, logger,
	)
	if err := server.Start(); err != nil {
		t.Fatalf("start worker mux: %v", err)
	}
	t.Cleanup(server.Shutdown)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: parsed.Host})
	t.Cleanup(func() { _ = client.Close() })
	sessionID := uuid.New()
	for _, task := range []*asynq.Task{
		asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(`{"sessionId":"`+sessionID.String()+`"}`)),
		queueadapter.NewSessionExpiryTask(),
		queueadapter.NewStorageCleanupTask(),
	} {
		if _, err := client.Enqueue(task); err != nil {
			t.Fatalf("enqueue %s: %v", task.Type(), err)
		}
	}
	select {
	case id := <-providers:
		if id != sessionID {
			t.Fatalf("provider received %s, want %s", id, sessionID)
		}
	case <-ctx.Done():
		t.Fatal("provider task did not reach provider handler")
	}
	select {
	case <-expiries:
	case <-ctx.Done():
		t.Fatal("expiry task did not reach expiry handler")
	}
	select {
	case input := <-cleanups:
		if input.Limit != 7 {
			t.Fatalf("cleanup limit = %d, want 7", input.Limit)
		}
	case <-ctx.Done():
		t.Fatal("cleanup task did not reach cleanup handler")
	}
}
