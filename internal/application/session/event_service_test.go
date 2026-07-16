package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

func TestTransitionCommitsGuardedStateAndMappedActionEventTogether(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	transactions := newMemoryEventTransactions(id)
	service := session.NewEventService(transactions, fixedClock{now: now})

	err := service.Transition(context.Background(), session.TransitionParams{
		SessionID:     id,
		ExpectedState: verificationsession.Created,
		Action:        sessionevent.SubmitPersonalDetails,
	})
	if err != nil {
		t.Fatalf("Transition() error = %v", err)
	}

	if transactions.status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("committed state = %q, want %q", transactions.status, session.StatusPersonalDetailsSubmitted)
	}
	if len(transactions.events) != 1 || transactions.events[0].Type != sessionevent.SubmitPersonalDetails {
		t.Errorf("committed events = %#v, want one submit_personal_details event", transactions.events)
	}
}

func TestExpireCommitsExpiredStateAndExpireActionAtDeadline(t *testing.T) {
	deadline := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	transactions := newMemoryEventTransactions(id)
	service := session.NewEventService(transactions, fixedClock{now: deadline})

	err := service.Expire(context.Background(), session.ExpireParams{
		SessionID:     id,
		ExpectedState: verificationsession.Created,
		Deadline:      deadline,
	})
	if err != nil {
		t.Fatalf("Expire() error = %v", err)
	}

	if transactions.status != session.StatusExpired {
		t.Errorf("committed state = %q, want %q", transactions.status, verificationsession.Expired)
	}
	if len(transactions.events) != 1 || transactions.events[0].Type != sessionevent.Expire {
		t.Errorf("committed events = %#v, want one expire event", transactions.events)
	}
}

func TestRejectedTransitionsLeaveStateAndEventsUnchanged(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	tests := []struct {
		name      string
		stored    verificationsession.State
		expected  verificationsession.State
		action    sessionevent.Type
		wantError error
	}{
		{"forbidden", verificationsession.Created, verificationsession.Created, sessionevent.SubmitSession, verificationsession.ErrTransitionForbidden},
		{"terminal", verificationsession.Verified, verificationsession.Verified, sessionevent.SubmitPersonalDetails, verificationsession.ErrTerminalState},
		{"stale", verificationsession.PersonalDetailsSubmitted, verificationsession.Created, sessionevent.SubmitPersonalDetails, session.ErrSessionTransitionStale},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactions := newMemoryEventTransactions(id)
			transactions.status = tt.stored
			service := session.NewEventService(transactions, fixedClock{now: now})

			err := service.Transition(context.Background(), session.TransitionParams{
				SessionID: id, ExpectedState: tt.expected, Action: tt.action,
			})
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Transition() error = %v, want %v", err, tt.wantError)
			}
			if transactions.status != tt.stored {
				t.Errorf("state = %q, want unchanged %q", transactions.status, tt.stored)
			}
			if len(transactions.events) != 0 {
				t.Errorf("events = %d, want none", len(transactions.events))
			}
		})
	}
}

func TestExpiryBeforeDeadlineLeavesStateAndEventsUnchanged(t *testing.T) {
	deadline := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	id := uuid.MustParse("d20a3890-165b-4f9e-a392-4fb323a6237e")
	transactions := newMemoryEventTransactions(id)
	service := session.NewEventService(transactions, fixedClock{now: deadline.Add(-time.Nanosecond)})

	err := service.Expire(context.Background(), session.ExpireParams{
		SessionID: id, ExpectedState: verificationsession.Created, Deadline: deadline,
	})
	if !errors.Is(err, verificationsession.ErrDeadlineNotReached) {
		t.Fatalf("Expire() error = %v, want ErrDeadlineNotReached", err)
	}
	if transactions.status != verificationsession.Created || len(transactions.events) != 0 {
		t.Errorf("rejected expiry committed state %q and %d events", transactions.status, len(transactions.events))
	}
}

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

func (m *memoryEventTransaction) UpdateState(
	_ context.Context,
	id uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	_ time.Time,
) error {
	if id != m.sessionID || verificationsession.State(m.status) != expected {
		return session.ErrSessionTransitionStale
	}
	m.status = session.Status(next)
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
