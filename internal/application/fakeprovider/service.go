package fakeprovider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ProviderSubmissionRequest struct {
	SessionID        uuid.UUID                    `json:"sessionId"`
	CallbackURL      string                       `json:"callbackUrl"`
	PersonalDetails  PersonalDetails              `json:"personalDetails"`
	IdentityDocument VerificationArtifactMetadata `json:"identityDocument"`
	BiometricCapture VerificationArtifactMetadata `json:"biometricCapture"`
}

type PersonalDetails struct {
	FullName       string `json:"fullName"`
	DateOfBirth    string `json:"dateOfBirth"`
	IdentityNumber string `json:"identityNumber"`
	Address        string `json:"address"`
}

type VerificationArtifactMetadata struct {
	Kind        string `json:"kind"`
	StorageKey  string `json:"storageKey"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	ETag        string `json:"eTag"`
}

type Verdict string

const (
	Verified Verdict = "verified"
	Rejected Verdict = "rejected"

	MaxDuplicateCallbacks = 10
)

type RejectionReason string

const (
	DocumentInvalid     RejectionReason = "document_invalid"
	BiometricMismatch   RejectionReason = "biometric_mismatch"
	IdentityNotVerified RejectionReason = "identity_not_verified"
	SuspectedFraud      RejectionReason = "suspected_fraud"
)

type Scenario struct {
	Verdict            Verdict         `json:"verdict"`
	Reason             RejectionReason `json:"reason"`
	DelayMs            int             `json:"delayMs"`
	DuplicateCallbacks int             `json:"duplicateCallbacks"`
}

func (s Scenario) Validate() error {
	if s.DelayMs < 0 || s.DuplicateCallbacks < 0 || s.DuplicateCallbacks > MaxDuplicateCallbacks {
		return ErrInvalidScenario
	}

	switch s.Verdict {
	case Verified:
		if s.Reason != "" {
			return ErrInvalidScenario
		}
	case Rejected:
		switch s.Reason {
		case DocumentInvalid, BiometricMismatch, IdentityNotVerified, SuspectedFraud:
		default:
			return ErrInvalidScenario
		}
	default:
		return ErrInvalidScenario
	}

	return nil
}

type WebhookEvent struct {
	EventID   uuid.UUID       `json:"eventId"`
	SessionID uuid.UUID       `json:"sessionId"`
	Verdict   Verdict         `json:"verdict"`
	Reason    RejectionReason `json:"reason,omitempty"`
}

type ScenarioStore interface {
	Lookup(sessionID uuid.UUID) Scenario
}

type Callback interface {
	Send(ctx context.Context, callbackURL string, event WebhookEvent) error
}

type CallbackDelay interface {
	Wait(ctx context.Context, delay time.Duration) error
}

type CallbackFailure struct {
	EventID   uuid.UUID
	SessionID uuid.UUID
	Err       error
}

type CallbackFailureReporter interface {
	ReportCallbackFailure(failure CallbackFailure)
}

type acceptedSubmission struct {
	Request  ProviderSubmissionRequest
	Scenario Scenario
	EventID  uuid.UUID
}

type Service struct {
	mu              sync.Mutex
	submissions     map[uuid.UUID]*acceptedSubmission
	scenarios       ScenarioStore
	callbackSender  Callback
	callbackTimeout time.Duration
	failureReporter CallbackFailureReporter
	callbackDelay   CallbackDelay
	callbacks       sync.WaitGroup
	closing         bool
}

func NewService(
	scenarios ScenarioStore,
	callbackSender Callback,
	callbackTimeout time.Duration,
	failureReporter CallbackFailureReporter,
	callbackDelay CallbackDelay,
) *Service {
	if callbackDelay == nil {
		callbackDelay = timerCallbackDelay{}
	}
	return &Service{
		submissions:     map[uuid.UUID]*acceptedSubmission{},
		scenarios:       scenarios,
		callbackSender:  callbackSender,
		callbackTimeout: callbackTimeout,
		failureReporter: failureReporter,
		callbackDelay:   callbackDelay,
	}
}

var (
	ErrInvalidScenario        = errors.New("invalid fake provider scenario")
	ErrInvalidSessionID       = errors.New("invalid session id")
	ErrIdempotencyKeyMismatch = errors.New("idempotency key mismatch")
	ErrIdempotencyConflict    = errors.New("idempotency conflicted")
	ErrServiceShuttingDown    = errors.New("fake provider service is shutting down")
)

func (s *Service) Accept(ctx context.Context, idempotencyKey uuid.UUID, request ProviderSubmissionRequest) (bool, error) {
	scenario := s.scenarios.Lookup(request.SessionID)

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return false, ErrServiceShuttingDown
	}

	existing, found := s.submissions[idempotencyKey]
	if found {
		s.mu.Unlock()
		if existing.Request != request {
			return false, ErrIdempotencyConflict
		}
		return true, nil
	}

	accepted := &acceptedSubmission{
		Request:  request,
		Scenario: scenario,
		EventID:  uuid.New(),
	}

	s.submissions[idempotencyKey] = accepted

	s.callbacks.Add(1)
	s.mu.Unlock()

	s.startCallback(accepted)

	return false, nil
}

func (s *Service) startCallback(submission *acceptedSubmission) {
	go func() {
		defer s.callbacks.Done()

		if err := s.processCallback(submission); err != nil {
			if s.failureReporter != nil {
				s.failureReporter.ReportCallbackFailure(CallbackFailure{
					EventID:   submission.EventID,
					SessionID: submission.Request.SessionID,
					Err:       err,
				})
			}
		}
	}()
}

func (s *Service) processCallback(submission *acceptedSubmission) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.callbackTimeout)
	defer cancel()
	if err := s.callbackDelay.Wait(ctx, time.Duration(submission.Scenario.DelayMs)*time.Millisecond); err != nil {
		return err
	}

	event := WebhookEvent{
		EventID:   submission.EventID,
		SessionID: submission.Request.SessionID,
		Verdict:   submission.Scenario.Verdict,
		Reason:    submission.Scenario.Reason,
	}
	for range submission.Scenario.DuplicateCallbacks + 1 {
		if err := s.callbackSender.Send(ctx, submission.Request.CallbackURL, event); err != nil {
			return err
		}
	}
	return nil
}

type timerCallbackDelay struct{}

func (timerCallbackDelay) Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.callbacks.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
