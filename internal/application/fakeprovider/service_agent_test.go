package fakeprovider_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/fakeprovider"
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
		nil,
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

func TestServiceDelaysCallbackWithoutBlockingAcceptance(t *testing.T) {
	sessionID := uuid.MustParse("ec5ceada-c575-469c-8a53-ed56f7488310")
	delay := &controlledCallbackDelay{
		started: make(chan time.Duration, 1),
		release: make(chan struct{}),
	}
	callback := &recordingCallbackSender{events: make(chan fakeprovider.WebhookEvent, 1)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{
			Verdict: fakeprovider.Verified,
			DelayMs: 50,
		}},
		callback,
		time.Second,
		nil,
		delay,
	)

	replayed, err := service.Accept(context.Background(), sessionID, fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: "https://callback.example.test/webhooks/verification",
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if replayed {
		t.Fatal("Accept() replayed = true for first submission")
	}

	select {
	case got := <-delay.started:
		if got != 50*time.Millisecond {
			t.Fatalf("callback delay = %s, want %s", got, 50*time.Millisecond)
		}
	case <-time.After(time.Second):
		t.Fatal("callback delay did not start")
	}
	select {
	case event := <-callback.events:
		t.Fatalf("callback sent before delay release: %#v", event)
	default:
	}

	close(delay.release)
	select {
	case event := <-callback.events:
		if event.SessionID != sessionID || event.Verdict != fakeprovider.Verified {
			t.Fatalf("callback event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("callback was not sent after delay release")
	}
}

func TestServiceSendsConfiguredDuplicatesWithSameLogicalEvent(t *testing.T) {
	sessionID := uuid.MustParse("f3fa8254-f3b2-41d0-bf8c-15d14f700f94")
	callback := &recordingCallbackSender{events: make(chan fakeprovider.WebhookEvent, 4)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{
			Verdict:            fakeprovider.Rejected,
			Reason:             fakeprovider.BiometricMismatch,
			DuplicateCallbacks: 2,
		}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)

	_, err := service.Accept(context.Background(), sessionID, fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: "https://callback.example.test/webhooks/verification",
	})
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}

	var callbacks []fakeprovider.WebhookEvent
	for range 3 {
		select {
		case event := <-callback.events:
			callbacks = append(callbacks, event)
		case <-time.After(time.Second):
			t.Fatal("configured callback sequence did not complete")
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(callback.events) != 0 {
		t.Fatalf("unexpected callback beyond configured duplicate count: %d", len(callback.events))
	}
	for index, event := range callbacks {
		if event != callbacks[0] {
			t.Fatalf("callback %d = %#v, want exact logical event %#v", index, event, callbacks[0])
		}
	}
	if callbacks[0].EventID == uuid.Nil || callbacks[0].SessionID != sessionID || callbacks[0].Verdict != fakeprovider.Rejected || callbacks[0].Reason != fakeprovider.BiometricMismatch {
		t.Fatalf("callback event = %#v", callbacks[0])
	}
}

func TestServiceSameSubmissionReplayDoesNotStartAnotherCallback(t *testing.T) {
	sessionID := uuid.MustParse("29672f1a-edf9-48f0-976b-fb65764acaf7")
	request := fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: "https://callback.example.test/webhooks/verification",
	}
	callback := &recordingCallbackSender{events: make(chan fakeprovider.WebhookEvent, 2)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)

	firstReplay, err := service.Accept(context.Background(), sessionID, request)
	if err != nil || firstReplay {
		t.Fatalf("first Accept() = replay %t, error %v", firstReplay, err)
	}
	secondReplay, err := service.Accept(context.Background(), sessionID, request)
	if err != nil || !secondReplay {
		t.Fatalf("second Accept() = replay %t, error %v", secondReplay, err)
	}

	assertOneLogicalCallback(t, service, callback.events, sessionID)
}

func TestServiceSameKeyDifferentSubmissionConflictsWithoutAnotherCallback(t *testing.T) {
	sessionID := uuid.MustParse("4892cbb4-b807-4923-bbf5-6ad141522be2")
	callback := &recordingCallbackSender{events: make(chan fakeprovider.WebhookEvent, 2)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)
	first := fakeprovider.ProviderSubmissionRequest{SessionID: sessionID, CallbackURL: "https://callback.example.test/first"}
	second := first
	second.CallbackURL = "https://callback.example.test/different"

	if replayed, err := service.Accept(context.Background(), sessionID, first); err != nil || replayed {
		t.Fatalf("first Accept() = replay %t, error %v", replayed, err)
	}
	if replayed, err := service.Accept(context.Background(), sessionID, second); !errors.Is(err, fakeprovider.ErrIdempotencyConflict) || replayed {
		t.Fatalf("different Accept() = replay %t, error %v, want conflict", replayed, err)
	}

	assertOneLogicalCallback(t, service, callback.events, sessionID)
}

func TestServiceConcurrentSameSubmissionStartsOneLogicalCallback(t *testing.T) {
	sessionID := uuid.MustParse("7df966e1-ac40-448a-9e94-3500324c0265")
	request := fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: "https://callback.example.test/webhooks/verification",
	}
	callback := &recordingCallbackSender{events: make(chan fakeprovider.WebhookEvent, 2)}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)

	const actors = 20
	start := make(chan struct{})
	results := make(chan struct {
		replayed bool
		err      error
	}, actors)
	var actorsDone sync.WaitGroup
	actorsDone.Add(actors)
	for range actors {
		go func() {
			defer actorsDone.Done()
			<-start
			replayed, err := service.Accept(context.Background(), sessionID, request)
			results <- struct {
				replayed bool
				err      error
			}{replayed: replayed, err: err}
		}()
	}
	close(start)
	actorsDone.Wait()
	close(results)

	initialCount := 0
	replayCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Accept() error = %v", result.err)
		}
		if result.replayed {
			replayCount++
		} else {
			initialCount++
		}
	}
	if initialCount != 1 || replayCount != actors-1 {
		t.Fatalf("initial/replay counts = %d/%d, want 1/%d", initialCount, replayCount, actors-1)
	}
	assertOneLogicalCallback(t, service, callback.events, sessionID)
}

func TestServiceShutdownRejectsNewWorkAndWaitsForActiveCallback(t *testing.T) {
	sessionID := uuid.MustParse("9062ab3c-2e46-4289-b26a-7741a3f2086e")
	callback := &blockingCallbackSender{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)
	request := fakeprovider.ProviderSubmissionRequest{SessionID: sessionID, CallbackURL: "https://callback.example.test/wait"}
	if _, err := service.Accept(context.Background(), sessionID, request); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	select {
	case <-callback.started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdownResults := make(chan error, 1)
	go func() { shutdownResults <- service.Shutdown(shutdownCtx) }()
	for {
		_, err := service.Accept(context.Background(), sessionID, request)
		if errors.Is(err, fakeprovider.ErrServiceShuttingDown) {
			break
		}
		if err != nil {
			t.Fatalf("Accept() while shutdown starts error = %v", err)
		}
		runtime.Gosched()
	}
	select {
	case err := <-shutdownResults:
		t.Fatalf("Shutdown() completed before callback release: %v", err)
	default:
	}

	close(callback.release)
	select {
	case err := <-shutdownResults:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown() did not complete after callback release")
	}
}

func TestServiceShutdownIsBoundedWhenCallbackDoesNotFinish(t *testing.T) {
	sessionID := uuid.MustParse("da216d14-7c98-43ff-8ff6-35c94602741e")
	callback := &blockingCallbackSender{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(callback.release) })
	service := fakeprovider.NewService(
		fixedApplicationScenarioStore{scenario: fakeprovider.Scenario{Verdict: fakeprovider.Verified}},
		callback,
		time.Second,
		nil,
		immediateCallbackDelay{},
	)
	if _, err := service.Accept(context.Background(), sessionID, fakeprovider.ProviderSubmissionRequest{SessionID: sessionID}); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	select {
	case <-callback.started:
	case <-time.After(time.Second):
		t.Fatal("callback did not start")
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Shutdown(shutdownCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v, want context canceled", err)
	}
}

func assertOneLogicalCallback(t *testing.T, service *fakeprovider.Service, events <-chan fakeprovider.WebhookEvent, sessionID uuid.UUID) {
	t.Helper()
	select {
	case event := <-events:
		if event.EventID == uuid.Nil || event.SessionID != sessionID {
			t.Fatalf("callback event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("logical callback was not sent")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("callback count exceeded one logical effect: %d queued", len(events))
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

type controlledCallbackDelay struct {
	started chan time.Duration
	release chan struct{}
}

func (d *controlledCallbackDelay) Wait(ctx context.Context, delay time.Duration) error {
	d.started <- delay
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type recordingCallbackSender struct {
	events chan fakeprovider.WebhookEvent
}

type immediateCallbackDelay struct{}

func (immediateCallbackDelay) Wait(context.Context, time.Duration) error { return nil }

type blockingCallbackSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingCallbackSender) Send(ctx context.Context, _ string, _ fakeprovider.WebhookEvent) error {
	s.started <- struct{}{}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *recordingCallbackSender) Send(_ context.Context, _ string, event fakeprovider.WebhookEvent) error {
	s.events <- event
	return nil
}

func (r *recordingCallbackFailureReporter) ReportCallbackFailure(failure fakeprovider.CallbackFailure) {
	r.failures <- failure
}
