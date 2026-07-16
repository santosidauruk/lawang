package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

func TestRecordPersonalDetailsSubmissionCommitsStateAndEventTogether(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	transactions := newMemoryEventTransactions(id)
	service := session.NewEventService(transactions, fixedClock{now: now})

	if err := service.RecordPersonalDetailsSubmission(context.Background(), id); err != nil {
		t.Fatalf("RecordPersonalDetailsSubmission() error = %v", err)
	}

	if transactions.status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("committed status = %q, want %q", transactions.status, session.StatusPersonalDetailsSubmitted)
	}
	if len(transactions.events) != 1 {
		t.Fatalf("committed events = %d, want 1", len(transactions.events))
	}
	event := transactions.events[0]
	if event.SessionID != id || event.Type != sessionevent.SubmitPersonalDetails || event.OccurredAt != now {
		t.Errorf("committed event = %#v, want submit_personal_details for session at fixed time", event)
	}
}

func TestRecordPersonalDetailsSubmissionRollsBackStateWhenEventAppendFails(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	appendFailure := errors.New("forced append failure")
	transactions := newMemoryEventTransactions(id)
	transactions.appendErr = appendFailure
	service := session.NewEventService(transactions, fixedClock{now: now})

	err := service.RecordPersonalDetailsSubmission(context.Background(), id)
	if !errors.Is(err, appendFailure) {
		t.Fatalf("RecordPersonalDetailsSubmission() error = %v, want forced append failure", err)
	}
	if transactions.status != session.StatusCreated {
		t.Errorf("status after rollback = %q, want %q", transactions.status, session.StatusCreated)
	}
	if len(transactions.events) != 0 {
		t.Errorf("events after rollback = %d, want 0", len(transactions.events))
	}
}

type memoryEventTransactions struct {
	sessionID uuid.UUID
	status    session.Status
	events    []session.AppendEventParams
	appendErr error
}

func newMemoryEventTransactions(id uuid.UUID) *memoryEventTransactions {
	return &memoryEventTransactions{sessionID: id, status: session.StatusCreated}
}

func (m *memoryEventTransactions) WithinTransaction(
	ctx context.Context,
	operation func(session.EventTransaction) error,
) error {
	tx := &memoryEventTransaction{sessionID: m.sessionID, status: m.status, appendErr: m.appendErr}
	if err := operation(tx); err != nil {
		return err
	}
	m.status = tx.status
	m.events = append(m.events, tx.events...)
	return nil
}

type memoryEventTransaction struct {
	sessionID uuid.UUID
	status    session.Status
	events    []session.AppendEventParams
	appendErr error
}

func (m *memoryEventTransaction) MarkPersonalDetailsSubmitted(
	_ context.Context,
	id uuid.UUID,
	_ time.Time,
) error {
	if id != m.sessionID || m.status != session.StatusCreated {
		return session.ErrSessionNotFound
	}
	m.status = session.StatusPersonalDetailsSubmitted
	return nil
}

func (m *memoryEventTransaction) AppendEvent(
	_ context.Context,
	params session.AppendEventParams,
) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	m.events = append(m.events, params)
	return nil
}
