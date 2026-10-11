package queue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/outbox"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
)

type providerTaskServiceStub struct {
	err   error
	calls int
}

func TestProviderSubmissionHandlerClassifiesServiceFailures(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		serviceError  error
		expectedError error
		expectRetry   bool
	}{
		{name: "missing immutable records", serviceError: providersubmission.ErrImmutableRecordsMissing, expectedError: providersubmission.ErrImmutableRecordsMissing},
		{name: "permanent provider response", serviceError: providersubmission.ErrPermanentProvider, expectedError: providersubmission.ErrPermanentProvider},
		{name: "invalid task", serviceError: providersubmission.ErrInvalidTask, expectedError: providersubmission.ErrInvalidTask},
		{name: "transient provider response", serviceError: providersubmission.ErrTransientProvider, expectedError: providersubmission.ErrTransientProvider, expectRetry: true},
		{name: "cancellation", serviceError: context.Canceled, expectedError: context.Canceled, expectRetry: true},
	}

	payload := []byte(`{"sessionId":"` + uuid.NewString() + `"}`)
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			service := &providerTaskServiceStub{err: testCase.serviceError}
			handler := queue.NewProviderSubmissionHandler(service)

			err := handler.ProcessTask(context.Background(), asynq.NewTask(outbox.TaskTypeProviderSubmit, payload))
			if !errors.Is(err, testCase.expectedError) {
				t.Fatalf("expected wrapped %v, got %v", testCase.expectedError, err)
			}
			if errors.Is(err, asynq.SkipRetry) == testCase.expectRetry {
				t.Fatalf("unexpected retry classification: retry=%t err=%v", testCase.expectRetry, err)
			}
			if service.calls != 1 {
				t.Fatalf("expected one service call, got %d", service.calls)
			}
		})
	}
}

func TestProviderRetryDelayIsDeterministicForNineRetries(t *testing.T) {
	t.Parallel()

	for retryCount := 1; retryCount <= queue.MaxProviderRetries; retryCount++ {
		want := time.Second * time.Duration(1<<(retryCount-1))
		if got := queue.ProviderSubmitRetryDelay(retryCount, errors.New("sensitive"), nil); got != want {
			t.Fatalf("retry %d: expected %s, got %s", retryCount, want, got)
		}
	}

	if got := queue.ProviderSubmitRetryDelay(0, nil, nil); got != time.Second {
		t.Fatalf("expected retry count zero to clamp to 1s, got %s", got)
	}
	if got := queue.ProviderSubmitRetryDelay(queue.MaxProviderRetries+1, nil, nil); got != 256*time.Second {
		t.Fatalf("expected retries above the limit to clamp to 256s, got %s", got)
	}
}

func TestTaskRetryDelayKeepsProviderPolicySeparate(t *testing.T) {
	t.Parallel()
	providerTask := asynq.NewTask(outbox.TaskTypeProviderSubmit, nil)
	cleanupTask := queue.NewStorageCleanupTask()
	for n := 1; n <= queue.MaxProviderRetries; n++ {
		want := time.Second * time.Duration(1<<(n-1))
		if got := queue.TaskRetryDelay(n, errors.New("retry"), providerTask); got != want {
			t.Fatalf("provider retry %d = %s, want %s", n, got, want)
		}
	}
	if got := queue.TaskRetryDelay(1, errors.New("retry"), cleanupTask); got < 15*time.Second || got > 75*time.Second {
		t.Fatalf("cleanup retry delay = %s, want Asynq default range", got)
	}
}

func (s *providerTaskServiceStub) SendToProvider(context.Context, uuid.UUID) error {
	s.calls++
	return s.err
}

func TestProviderSubmissionHandlerRejectsInvalidPayloadPermanently(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		task *asynq.Task
	}{
		{name: "unsupported task type", task: asynq.NewTask("unexpected", nil)},
		{name: "malformed JSON", task: asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(`{"sessionId":`))},
		{name: "missing session ID", task: asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(`{}`))},
		{name: "unknown field", task: asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(`{"sessionId":"`+uuid.NewString()+`","extra":true}`))},
		{name: "trailing JSON", task: asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(`{"sessionId":"`+uuid.NewString()+`"}{}`))},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			service := &providerTaskServiceStub{}
			handler := queue.NewProviderSubmissionHandler(service)

			err := handler.ProcessTask(context.Background(), testCase.task)
			if !errors.Is(err, asynq.SkipRetry) {
				t.Fatalf("expected permanent SkipRetry error, got %v", err)
			}
			if service.calls != 0 {
				t.Fatalf("expected service not to be called, got %d calls", service.calls)
			}
		})
	}
}
