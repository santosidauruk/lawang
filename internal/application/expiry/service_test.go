package expiry_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

// TestExpirePendingSessionAtItsProviderDeadline is the fixed-clock application
// tracer for Issue 010 Checkpoint 1.
//
// Keep this as one behavior: the operation locks a verification_pending session,
// receives its distinct verification_deadline_at and older applicant expires_at,
// applies the domain expiry rule at the provider boundary, and commits one expired
// state plus one safe expire event at the application time. The no-fallback proof
// needs the not-due sibling case and remains agent-owned after review, together with
// missing-deadline, terminal, rollback, and replay cases.
func TestExpirePendingSessionAtItsProviderDeadline(t *testing.T) {
	providerDeadline := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("2298fb3e-c0e7-4d45-8bf1-861951ed5e3f")
	transactions := newMemoryExpiryTransactions(session.VerificationSession{
		ID:                     sessionID,
		Status:                 verificationsession.VerificationPending,
		ExpiresAt:              providerDeadline.Add(-23 * time.Hour),
		VerificationDeadlineAt: &providerDeadline,
		UpdatedAt:              providerDeadline.Add(-24 * time.Hour),
	})
	service := expiry.NewService(transactions, fixedClock{now: providerDeadline})

	got, err := service.Expire(context.Background(), expiry.Input{SessionID: sessionID})
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if got.ID != sessionID || got.Status != verificationsession.Expired || !got.Changed {
		t.Errorf("Expire() result = %#v, want changed expired result for %s", got, sessionID)
	}
	if transactions.state.session.Status != verificationsession.Expired ||
		!transactions.state.session.UpdatedAt.Equal(providerDeadline) {
		t.Errorf(
			"stored session = status:%q updated_at:%s, want expired at %s",
			transactions.state.session.Status,
			transactions.state.session.UpdatedAt,
			providerDeadline,
		)
	}
	if len(transactions.state.events) != 1 {
		t.Fatalf("stored events = %d, want 1", len(transactions.state.events))
	}
	event := transactions.state.events[0]
	if event.SessionID != sessionID ||
		event.Type != sessionevent.Expire ||
		event.Metadata.Outcome() != "" ||
		!event.OccurredAt.Equal(providerDeadline) {
		t.Errorf("stored event = %#v, want one safe expire event at %s", event, providerDeadline)
	}
}

type memoryExpiryState struct {
	session session.VerificationSession
	events  []session.AppendEventParams
}

type memoryExpiryTransactions struct {
	state memoryExpiryState
}

func newMemoryExpiryTransactions(stored session.VerificationSession) *memoryExpiryTransactions {
	return &memoryExpiryTransactions{state: memoryExpiryState{session: stored}}
}

func (m *memoryExpiryTransactions) WithinTransaction(
	ctx context.Context,
	operation func(expiry.Transaction) error,
) error {
	working := memoryExpiryState{
		session: m.state.session,
		events:  append([]session.AppendEventParams(nil), m.state.events...),
	}
	if err := operation(&memoryExpiryTransaction{state: &working}); err != nil {
		return err
	}
	m.state = working
	return nil
}

type memoryExpiryTransaction struct {
	state *memoryExpiryState
}

func (m *memoryExpiryTransaction) LockSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if sessionID != m.state.session.ID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.state.session, nil
}

func (m *memoryExpiryTransaction) GuardExpiry(
	_ context.Context,
	sessionID uuid.UUID,
	expected verificationsession.State,
	expiredAt time.Time,
) (bool, error) {
	if sessionID != m.state.session.ID || m.state.session.Status != expected {
		return false, nil
	}
	m.state.session.Status = verificationsession.Expired
	m.state.session.UpdatedAt = expiredAt
	return true, nil
}

func (m *memoryExpiryTransaction) AppendEvent(
	_ context.Context,
	event session.AppendEventParams,
) error {
	m.state.events = append(m.state.events, event)
	return nil
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }
