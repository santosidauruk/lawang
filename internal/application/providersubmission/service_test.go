package providersubmission_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

func TestSubmitFirstEligibleSessionReturnsPendingAndCommitsOneOutcome(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("63f04b34-cf47-47a5-b501-cb734bf8ce36")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID:              sessionID,
		Status:          session.StatusBiometricCaptureUploaded,
		ResumeTokenHash: []byte("stored-token-hash"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	service := providersubmission.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-token-hash"), equal: true},
		fixedClock{now: now},
	)

	got, err := service.Submit(context.Background(), sessionID, "raw-resume-token")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.ID != sessionID || got.Status != session.StatusVerificationPending || got.Replayed {
		t.Errorf("Submit() result = %#v, want first verification_pending result", got)
	}
	if transactions.state.session.Status != session.StatusVerificationPending {
		t.Errorf("stored status = %q, want verification_pending", transactions.state.session.Status)
	}
	wantDeadline := now.Add(24 * time.Hour)
	if transactions.state.session.VerificationDeadlineAt == nil ||
		!transactions.state.session.VerificationDeadlineAt.Equal(wantDeadline) {
		t.Errorf(
			"stored verification deadline = %v, want %s",
			transactions.state.session.VerificationDeadlineAt,
			wantDeadline,
		)
	}
	if len(transactions.state.events) != 1 ||
		transactions.state.events[0].Type != sessionevent.SubmitSession ||
		transactions.state.events[0].Metadata.Outcome() != "" {
		t.Errorf("stored events = %#v, want one safe submit_session", transactions.state.events)
	}
	if transactions.state.outboxWrites != 1 || transactions.state.outboxID == uuid.Nil {
		t.Errorf(
			"outbox writes = %d with id %s, want one application-generated id",
			transactions.state.outboxWrites,
			transactions.state.outboxID,
		)
	}
}

func TestSubmitPendingSessionReturnsDurableReplayWithoutMutation(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("63f04b34-cf47-47a5-b501-cb734bf8ce36")
	deadline := now.Add(23 * time.Hour)
	transactions := newMemoryTransactions(session.VerificationSession{
		ID:                     sessionID,
		Status:                 session.StatusVerificationPending,
		ResumeTokenHash:        []byte("stored-token-hash"),
		ExpiresAt:              now.Add(-time.Minute),
		VerificationDeadlineAt: &deadline,
	})
	transactions.state.outboxWrites = 1
	transactions.state.outboxID = uuid.MustParse("8991dbd4-10ba-4a20-80d3-f9d0ce9bc956")
	service := providersubmission.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-token-hash"), equal: true},
		fixedClock{now: now},
	)

	got, err := service.Submit(context.Background(), sessionID, "raw-resume-token")
	if err != nil {
		t.Fatalf("Submit() replay error = %v", err)
	}
	if got.ID != sessionID || got.Status != session.StatusVerificationPending || !got.Replayed {
		t.Errorf("Submit() replay result = %#v, want durable replay", got)
	}
	if transactions.readinessCalls != 0 || transactions.stateUpdates != 0 ||
		len(transactions.state.events) != 0 || transactions.state.outboxWrites != 1 {
		t.Errorf(
			"replay effects = readiness:%d state:%d events:%d outbox:%d, want no additional work",
			transactions.readinessCalls,
			transactions.stateUpdates,
			len(transactions.state.events),
			transactions.state.outboxWrites,
		)
	}
	if transactions.state.session.VerificationDeadlineAt != &deadline {
		t.Errorf("replay replaced durable deadline: got %v want original %v", transactions.state.session.VerificationDeadlineAt, &deadline)
	}
}

type memoryState struct {
	session      session.VerificationSession
	events       []session.AppendEventParams
	outboxID     uuid.UUID
	outboxWrites int
}

type memoryTransactions struct {
	state           memoryState
	readiness       bool
	readinessCalls  int
	stateUpdates    int
	transactionRuns int
}

func newMemoryTransactions(stored session.VerificationSession) *memoryTransactions {
	return &memoryTransactions{state: memoryState{session: stored}, readiness: true}
}

func (m *memoryTransactions) WithinTransaction(
	ctx context.Context,
	operation func(providersubmission.Transaction) error,
) error {
	m.transactionRuns++
	return operation(&memoryTransaction{owner: m})
}

type memoryTransaction struct{ owner *memoryTransactions }

func (m *memoryTransaction) LockSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if sessionID != m.owner.state.session.ID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.owner.state.session, nil
}

func (m *memoryTransaction) HasRequiredAcceptedArtifacts(
	_ context.Context,
	_ uuid.UUID,
) (bool, error) {
	m.owner.readinessCalls++
	return m.owner.readiness, nil
}

func (m *memoryTransaction) SetPendingVerification(
	_ context.Context,
	submittedAt time.Time,
	_ uuid.UUID,
) error {
	m.owner.stateUpdates++
	deadline := submittedAt.Add(24 * time.Hour)
	m.owner.state.session.Status = session.StatusVerificationPending
	m.owner.state.session.UpdatedAt = submittedAt
	m.owner.state.session.VerificationDeadlineAt = &deadline
	return nil
}

func (m *memoryTransaction) InsertUnpublishedOutbox(
	_ context.Context,
	outboxID uuid.UUID,
	_ uuid.UUID,
) error {
	m.owner.state.outboxID = outboxID
	m.owner.state.outboxWrites++
	return nil
}

func (m *memoryTransaction) AppendEvent(
	_ context.Context,
	event session.AppendEventParams,
) error {
	m.owner.state.events = append(m.owner.state.events, event)
	return nil
}

type stubTokenIssuer struct {
	hash  []byte
	equal bool
}

func (*stubTokenIssuer) Issue() (string, []byte, error) { return "", nil, nil }
func (s *stubTokenIssuer) Hash(string) []byte           { return s.hash }
func (s *stubTokenIssuer) Equal([]byte, []byte) bool    { return s.equal }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
