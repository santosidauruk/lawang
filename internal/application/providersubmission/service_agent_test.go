package providersubmission_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestSubmitWithoutBothAcceptedArtifactsReturnsNotReadyWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 11, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("fe252dae-a08f-455f-966c-829afdc36b75")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID:              sessionID,
		Status:          session.StatusBiometricCaptureUploaded,
		ResumeTokenHash: []byte("stored-token-hash"),
		ExpiresAt:       now.Add(time.Minute),
	})
	transactions.readiness = false
	service := providersubmission.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-token-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Submit(context.Background(), sessionID, "raw-resume-token")
	var submissionError *providersubmission.Error
	if !errors.As(err, &submissionError) ||
		submissionError.Code != providersubmission.CodeSubmissionNotReady {
		t.Fatalf("Submit() error = %v, want SUBMISSION_NOT_READY", err)
	}
	if submissionError.ID != sessionID {
		t.Errorf("not-ready ID = %s, want %s", submissionError.ID, sessionID)
	}
	if transactions.stateUpdates != 0 || len(transactions.state.events) != 0 ||
		transactions.state.outboxWrites != 0 {
		t.Errorf(
			"not-ready effects = state:%d events:%d outbox:%d, want none",
			transactions.stateUpdates,
			len(transactions.state.events),
			transactions.state.outboxWrites,
		)
	}
}

func TestSubmitWrongAndTerminalStatesReturnIllegalTransitionWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 11, 30, 0, 0, time.UTC)
	for _, status := range []session.Status{
		session.StatusCreated,
		session.StatusPersonalDetailsSubmitted,
		session.StatusIdentityDocumentUploaded,
		session.StatusVerified,
		session.StatusRejected,
		session.StatusExpired,
	} {
		t.Run(status.String(), func(t *testing.T) {
			sessionID := uuid.New()
			transactions := newMemoryTransactions(session.VerificationSession{
				ID: sessionID, Status: status, ResumeTokenHash: []byte("stored-token-hash"),
				ExpiresAt: now.Add(time.Minute),
			})
			service := providersubmission.NewService(
				transactions,
				&stubTokenIssuer{hash: []byte("stored-token-hash"), equal: true},
				fixedClock{now: now},
			)

			_, err := service.Submit(context.Background(), sessionID, "raw-resume-token")
			var submissionError *providersubmission.Error
			if !errors.As(err, &submissionError) ||
				submissionError.Code != providersubmission.CodeIllegalTransition {
				t.Fatalf("Submit() error = %v, want ILLEGAL_TRANSITION", err)
			}
			if submissionError.ID != sessionID || submissionError.From != status ||
				submissionError.Action.String() != "submit_session" {
				t.Errorf("illegal-transition context = %#v", submissionError)
			}
			if transactions.readinessCalls != 0 || transactions.stateUpdates != 0 ||
				len(transactions.state.events) != 0 || transactions.state.outboxWrites != 0 {
				t.Errorf("wrong-state submission mutated transaction state")
			}
		})
	}
}

func TestSubmitAuthenticationAndApplicantExpiryFailuresDoNotReachReadiness(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		requested  uuid.UUID
		stored     session.VerificationSession
		tokenEqual bool
		wantCode   session.ErrorCode
	}{
		{
			name: "unknown session", requested: uuid.New(),
			stored:     session.VerificationSession{ID: uuid.New()},
			tokenEqual: true, wantCode: session.CodeSessionNotFound,
		},
		{
			name: "wrong token", requested: uuid.MustParse("6db20b66-e069-4f80-8b21-d361fd63906f"),
			stored: session.VerificationSession{
				ID:     uuid.MustParse("6db20b66-e069-4f80-8b21-d361fd63906f"),
				Status: session.StatusBiometricCaptureUploaded, ExpiresAt: now.Add(time.Minute),
			},
			wantCode: session.CodeInvalidResumeToken,
		},
		{
			name: "expired before first submission", requested: uuid.MustParse("4e86cc65-a8a3-44e6-a4ce-87d912c7b827"),
			stored: session.VerificationSession{
				ID:     uuid.MustParse("4e86cc65-a8a3-44e6-a4ce-87d912c7b827"),
				Status: session.StatusBiometricCaptureUploaded, ExpiresAt: now,
			},
			tokenEqual: true, wantCode: session.CodeSessionExpired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transactions := newMemoryTransactions(test.stored)
			service := providersubmission.NewService(
				transactions,
				&stubTokenIssuer{equal: test.tokenEqual},
				fixedClock{now: now},
			)

			_, err := service.Submit(context.Background(), test.requested, "raw-resume-token")
			var sessionError *session.Error
			if !errors.As(err, &sessionError) || sessionError.Code != test.wantCode {
				t.Fatalf("Submit() error = %v, want %s", err, test.wantCode)
			}
			if transactions.readinessCalls != 0 || transactions.stateUpdates != 0 ||
				len(transactions.state.events) != 0 || transactions.state.outboxWrites != 0 {
				t.Errorf("authentication/expiry failure mutated transaction state")
			}
		})
	}
}
