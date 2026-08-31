package fakeprovider

import (
	"context"
	"errors"
	"fmt"
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

type WebhookEvent struct {
	EventID   uuid.UUID       `json:"eventId"`
	SessionID uuid.UUID       `json:"sessionId"`
	Verdict   Verdict         `json:"verdict"`
	Reason    RejectionReason `json:"reason"`
}

type ScenarioStore interface {
	Lookup(sessionID uuid.UUID) Scenario
}

type Callback interface {
	Send(ctx context.Context, callbackURL string, event WebhookEvent) error
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
}

func NewService(scenarios ScenarioStore, callbackSender Callback, callbackTimeout time.Duration) *Service {
	return &Service{
		submissions:     map[uuid.UUID]*acceptedSubmission{},
		scenarios:       scenarios,
		callbackSender:  callbackSender,
		callbackTimeout: callbackTimeout,
	}
}

var (
	ErrInvalidSessionID       = errors.New("invalid session id")
	ErrIdempotencyKeyMismatch = errors.New("idempotency key mismatch")
	ErrIdempotencyConflict    = errors.New("idempotency conflicted")
)

func (s *Service) Accept(ctx context.Context, idempotencyKey uuid.UUID, request ProviderSubmissionRequest) (bool, error) {
	scenario := s.scenarios.Lookup(request.SessionID)

	s.mu.Lock()
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

	s.mu.Unlock()

	s.startCallback(accepted)

	return false, nil
}

func (s *Service) startCallback(submission *acceptedSubmission) {
	go func() {
		if err := s.processCallback(submission); err != nil {
			fmt.Printf("callback Failed: %v", err)
		}
	}()
}

func (s *Service) processCallback(submission *acceptedSubmission) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.callbackTimeout)
	defer cancel()

	return s.callbackSender.Send(ctx, submission.Request.CallbackURL, WebhookEvent{
		EventID:   submission.EventID,
		SessionID: submission.Request.SessionID,
		Verdict:   submission.Scenario.Verdict,
		Reason:    submission.Scenario.Reason,
	})
}
