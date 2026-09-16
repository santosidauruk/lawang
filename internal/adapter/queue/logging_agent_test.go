package queue

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
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
	for _, required := range []string{"provider task completed", outbox.TaskTypeProviderSubmit, `"outcome":"retry"`} {
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
