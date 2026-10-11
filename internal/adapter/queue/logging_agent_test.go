package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/outbox"
)

type failingTaskHandler struct{ err error }

func (h failingTaskHandler) ProcessTask(context.Context, *asynq.Task) error { return h.err }

func TestSafeTaskLoggingOmitsPayloadAndUnderlyingErrors(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	secretPayload := `{"sessionId":"payload-secret"}`
	secretError := errors.New("provider response contained upstream-secret")
	handler := safeTaskHandler{next: failingTaskHandler{err: secretError}, logger: logger}

	err := handler.ProcessTask(context.Background(), asynq.NewTask(outbox.TaskTypeProviderSubmit, []byte(secretPayload)))
	if !errors.Is(err, secretError) {
		t.Fatalf("expected underlying error to be returned, got %v", err)
	}

	logged := output.String()
	for _, forbidden := range []string{"payload-secret", "upstream-secret", secretPayload} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("safe task log leaked %q: %s", forbidden, logged)
		}
	}
	for _, required := range []string{"queue task completed", outbox.TaskTypeProviderSubmit, `"outcome":"retry"`} {
		if !strings.Contains(logged, required) {
			t.Fatalf("safe task log omitted %q: %s", required, logged)
		}
	}
}

func TestSafeAsynqLoggerDiscardsUntrustedArguments(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := safeAsynqLogger{logger: slog.New(slog.NewJSONHandler(&output, nil))}
	logger.Error("upstream-secret", []byte(`{"payload":"payload-secret"}`))

	logged := output.String()
	for _, forbidden := range []string{"upstream-secret", "payload-secret"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("safe queue log leaked %q: %s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, "queue internal event") {
		t.Fatalf("expected bounded queue event message, got %s", logged)
	}
}

func TestCleanupTaskLogContainsOnlyAllowlistedFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	err := errors.New("secret-bucket/secret-key")
	handler := safeTaskHandler{next: failingTaskHandler{err: err}, logger: logger}
	if got := handler.ProcessTask(context.Background(), NewStorageCleanupTask()); !errors.Is(got, err) {
		t.Fatalf("handler error = %v, want original failure", got)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode task log: %v", err)
	}
	allowed := map[string]bool{
		"time": true, "level": true, "msg": true,
		"task_id": true, "task_type": true, "attempt": true,
		"duration": true, "outcome": true,
	}
	for key := range event {
		if !allowed[key] {
			t.Fatalf("unexpected task log field %q: %s", key, output.String())
		}
	}
	if len(event) != len(allowed) {
		t.Fatalf("task log fields = %v, want %v", event, allowed)
	}
	if event["task_type"] != TaskTypeStorageCleanup || event["outcome"] != "retry" {
		t.Fatalf("unexpected cleanup event: %v", event)
	}
	if strings.Contains(output.String(), "secret-bucket") || strings.Contains(output.String(), "secret-key") {
		t.Fatalf("cleanup log leaked underlying storage error: %s", output.String())
	}
}
