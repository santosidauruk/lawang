package queue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

// Checkpoint 4 RED tracer. Production task construction, strict decoding, and
// handler behavior belong to Bagian user. The batch limit comes from worker
// configuration, so no object identity or mutable candidate data enters Redis.
func TestStorageCleanupTaskStrictPayloadAndServiceCall(t *testing.T) {
	const batchSize int32 = 7
	service := &cleanupTaskServiceRecorder{}
	handler := queue.NewStorageCleanupHandler(service, batchSize)
	task := queue.NewStorageCleanupTask()

	if task.Type() != queue.TaskTypeStorageCleanup {
		t.Fatalf("task type = %q, want %q", task.Type(), queue.TaskTypeStorageCleanup)
	}
	if got := string(task.Payload()); got != `{}` {
		t.Fatalf("task payload = %q, want exact empty object", got)
	}
	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("valid cleanup task: %v", err)
	}
	if len(service.inputs) != 1 || service.inputs[0] != (cleanup.Input{Limit: batchSize}) {
		t.Fatalf("service inputs = %#v, want one Run with configured limit %d", service.inputs, batchSize)
	}

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{name: "null", payload: `null`},
		{name: "unknown field", payload: `{"storageKey":"secret-object"}`},
		{name: "trailing JSON", payload: `{} {}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(service.inputs)
			err := handler.ProcessTask(context.Background(), asynq.NewTask(queue.TaskTypeStorageCleanup, []byte(tc.payload)))
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("malformed task error = %v, want SkipRetry", err)
			}
			if len(service.inputs) != before {
				t.Fatalf("malformed task invoked service: %#v", service.inputs)
			}
		})
	}
}

type cleanupTaskServiceRecorder struct {
	inputs []cleanup.Input
}

func (r *cleanupTaskServiceRecorder) Run(_ context.Context, input cleanup.Input) (cleanup.CleanupResult, error) {
	r.inputs = append(r.inputs, input)
	return cleanup.CleanupResult{}, nil
}

type cleanupTaskFailureService struct{ err error }

func (s cleanupTaskFailureService) Run(context.Context, cleanup.Input) (cleanup.CleanupResult, error) {
	return cleanup.CleanupResult{}, s.err
}

func TestStorageCleanupTaskFailureClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		serviceErr error
		wantSkip   bool
		wantErr    error
	}{
		{name: "transient S3 failure", serviceErr: errors.Join(cleanup.ErrTransientCleanup, errors.New("secret key"))},
		{name: "permanent application failure", serviceErr: errors.Join(cleanup.ErrPermanentCleanup, errors.New("secret key")), wantSkip: true},
		{name: "unknown dependency failure", serviceErr: errors.New("secret key")},
		{name: "cancelled", serviceErr: context.Canceled, wantErr: context.Canceled},
		{name: "deadline", serviceErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := queue.NewStorageCleanupHandler(cleanupTaskFailureService{err: tc.serviceErr}, 10).
				ProcessTask(context.Background(), queue.NewStorageCleanupTask())
			if err == nil {
				t.Fatal("failure silently succeeded")
			}
			if got := errors.Is(err, asynq.SkipRetry); got != tc.wantSkip {
				t.Fatalf("SkipRetry = %t, want %t: %v", got, tc.wantSkip, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && !tc.wantSkip && errors.Is(err, tc.serviceErr) {
				t.Fatal("raw service error escaped queue boundary")
			}
		})
	}
}

type concurrentCleanupService struct {
	entered chan struct{}
	release chan struct{}
}

func (s concurrentCleanupService) Run(context.Context, cleanup.Input) (cleanup.CleanupResult, error) {
	s.entered <- struct{}{}
	<-s.release
	return cleanup.CleanupResult{}, nil
}

func TestStorageCleanupHandlerAllowsConcurrentTasks(t *testing.T) {
	service := concurrentCleanupService{entered: make(chan struct{}, 2), release: make(chan struct{})}
	handler := queue.NewStorageCleanupHandler(service, 10)
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- handler.ProcessTask(context.Background(), queue.NewStorageCleanupTask()) }()
	}
	for range 2 {
		select {
		case <-service.entered:
		case <-time.After(time.Second):
			close(service.release)
			t.Fatal("second cleanup handler was serialized behind the first")
		}
	}
	close(service.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent cleanup task: %v", err)
		}
	}
}
