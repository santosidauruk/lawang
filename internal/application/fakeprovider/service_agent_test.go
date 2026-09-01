package fakeprovider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
)

func TestServiceReportsAsynchronousCallbackFailure(t *testing.T) {
	sessionID := uuid.MustParse("903ea3ee-4dfa-4ac2-bd46-700c6ea38f44")
	wantErr := errors.New("callback transport failed")
	reporter := &recordingCallbackFailureReporter{failures: make(chan fakeprovider.CallbackFailure, 1)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		failingCallbackSender{err: wantErr},
		time.Second,
		reporter,
	)

	_, err := service.Accept(context.Background(), sessionID, fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: "https://callback.example.test/webhooks/verification",
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	select {
	case failure := <-reporter.failures:
		if failure.EventID == uuid.Nil {
			t.Fatal("reported callback failure has nil event ID")
		}
		if failure.SessionID != sessionID {
			t.Fatalf("reported session ID = %s, want %s", failure.SessionID, sessionID)
		}
		if !errors.Is(failure.Err, wantErr) {
			t.Fatalf("reported error = %v, want %v", failure.Err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("callback failure was not reported")
	}
}

type fixedApplicationScenarioStore struct {
	scenario fakeprovider.Scenario
}

func (s fixedApplicationScenarioStore) Lookup(uuid.UUID) fakeprovider.Scenario {
	return s.scenario
}

type failingCallbackSender struct {
	err error
}

func (s failingCallbackSender) Send(context.Context, string, fakeprovider.WebhookEvent) error {
	return s.err
}

type recordingCallbackFailureReporter struct {
	failures chan fakeprovider.CallbackFailure
}

func (r *recordingCallbackFailureReporter) ReportCallbackFailure(failure fakeprovider.CallbackFailure) {
	r.failures <- failure
}
