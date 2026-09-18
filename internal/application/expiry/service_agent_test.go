package expiry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

func TestExpiryNotDueUsesOnlyTheApplicableDeadline(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Nanosecond)

	tests := []struct {
		name   string
		stored session.VerificationSession
	}{
		{
			name: "applicant stage ignores provider deadline",
			stored: session.VerificationSession{
				ID:     uuid.MustParse("f294314c-46a7-41ca-b18a-d81dd50eb8f8"),
				Status: verificationsession.Created, ExpiresAt: future,
				VerificationDeadlineAt: &past, UpdatedAt: now.Add(-time.Hour),
			},
		},
		{
			name: "pending provider ignores applicant expiry",
			stored: session.VerificationSession{
				ID:     uuid.MustParse("35668135-d374-4ed0-ae24-6cc6034885ac"),
				Status: verificationsession.VerificationPending, ExpiresAt: past,
				VerificationDeadlineAt: &future, UpdatedAt: now.Add(-time.Hour),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactions := newAgentExpiryTransactions(tt.stored)
			service := expiry.NewService(transactions, fixedClock{now: now})

			got, err := service.Expire(context.Background(), expiry.Input{SessionID: tt.stored.ID})
			if !errors.Is(err, verificationsession.ErrDeadlineNotReached) {
				t.Fatalf("Expire() error = %v, want ErrDeadlineNotReached", err)
			}
			if got != (expiry.Result{}) {
				t.Errorf("Expire() result = %#v, want empty result", got)
			}
			if transactions.guardCalls != 0 || transactions.appendCalls != 0 {
				t.Errorf("not-due writes = guard:%d append:%d, want 0/0", transactions.guardCalls, transactions.appendCalls)
			}
			if transactions.state.Status != tt.stored.Status || !transactions.state.UpdatedAt.Equal(tt.stored.UpdatedAt) {
				t.Errorf("not-due state = status:%q updated:%s, want unchanged", transactions.state.Status, transactions.state.UpdatedAt)
			}
		})
	}
}

func TestPendingExpiryWithoutProviderDeadlineFailsBeforeWrites(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	stored := session.VerificationSession{
		ID:        uuid.MustParse("16431d0d-eb19-44c3-8ad2-4f92cfd6b7e3"),
		Status:    verificationsession.VerificationPending,
		ExpiresAt: now.Add(-time.Hour),
		UpdatedAt: now.Add(-24 * time.Hour),
	}
	transactions := newAgentExpiryTransactions(stored)
	service := expiry.NewService(transactions, fixedClock{now: now})

	got, err := service.Expire(context.Background(), expiry.Input{SessionID: stored.ID})
	if err == nil {
		t.Fatal("Expire() error = nil, want missing provider deadline failure")
	}
	if got != (expiry.Result{}) {
		t.Errorf("Expire() result = %#v, want empty result", got)
	}
	if transactions.guardCalls != 0 || transactions.appendCalls != 0 {
		t.Errorf("missing-deadline writes = guard:%d append:%d, want 0/0", transactions.guardCalls, transactions.appendCalls)
	}
	if transactions.state.Status != stored.Status || !transactions.state.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Errorf("missing-deadline state = status:%q updated:%s, want unchanged", transactions.state.Status, transactions.state.UpdatedAt)
	}
}

func TestExpiryStaleGuardReturnsBoundedErrorWithoutEvent(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	stored := session.VerificationSession{
		ID:        uuid.MustParse("01a329ee-b9b5-4ae4-8458-5d538606edc6"),
		Status:    verificationsession.Created,
		ExpiresAt: now,
		UpdatedAt: now.Add(-30 * time.Minute),
	}
	transactions := newAgentExpiryTransactions(stored)
	transactions.guardResult = false
	service := expiry.NewService(transactions, fixedClock{now: now})

	got, err := service.Expire(context.Background(), expiry.Input{SessionID: stored.ID})
	if !errors.Is(err, session.ErrSessionTransitionStale) {
		t.Fatalf("Expire() error = %v, want ErrSessionTransitionStale", err)
	}
	if got != (expiry.Result{}) {
		t.Errorf("Expire() result = %#v, want empty result", got)
	}
	if transactions.guardCalls != 1 || transactions.appendCalls != 0 {
		t.Errorf("stale calls = guard:%d append:%d, want 1/0", transactions.guardCalls, transactions.appendCalls)
	}
	if transactions.state.Status != stored.Status || !transactions.state.UpdatedAt.Equal(stored.UpdatedAt) {
		t.Errorf("stale state = status:%q updated:%s, want unchanged", transactions.state.Status, transactions.state.UpdatedAt)
	}
}

func TestExpiryTerminalAndReplayCallsNeverWriteAnotherEvent(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		status    verificationsession.State
		wantError error
		want      expiry.Result
	}{
		{
			name:   "expired replay",
			status: verificationsession.Expired,
			want:   expiry.Result{Status: verificationsession.Expired, Changed: false},
		},
		{name: "verified remains terminal", status: verificationsession.Verified, wantError: verificationsession.ErrTerminalState},
		{name: "rejected remains terminal", status: verificationsession.Rejected, wantError: verificationsession.ErrTerminalState},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := session.VerificationSession{
				ID: uuid.New(), Status: tt.status,
				ExpiresAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
			}
			transactions := newAgentExpiryTransactions(stored)
			service := expiry.NewService(transactions, fixedClock{now: now})

			got, err := service.Expire(context.Background(), expiry.Input{SessionID: stored.ID})
			if tt.wantError == nil {
				if err != nil {
					t.Fatalf("Expire() error = %v", err)
				}
				tt.want.ID = stored.ID
				if got != tt.want {
					t.Errorf("Expire() result = %#v, want %#v", got, tt.want)
				}
			} else {
				if !errors.Is(err, tt.wantError) {
					t.Fatalf("Expire() error = %v, want %v", err, tt.wantError)
				}
				if got != (expiry.Result{}) {
					t.Errorf("Expire() result = %#v, want empty result", got)
				}
			}
			if transactions.guardCalls != 0 || transactions.appendCalls != 0 {
				t.Errorf("terminal/replay writes = guard:%d append:%d, want 0/0", transactions.guardCalls, transactions.appendCalls)
			}
			if transactions.state.Status != stored.Status || len(transactions.events) != 0 {
				t.Errorf("terminal/replay durable effects = status:%q events:%d, want unchanged", transactions.state.Status, len(transactions.events))
			}
		})
	}
}

func TestExpiryCommitFailureReturnsEmptyResultAndNoCommittedEffects(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	stored := session.VerificationSession{
		ID:        uuid.MustParse("64da4bba-177e-4807-9e88-a111a13ea9df"),
		Status:    verificationsession.Created,
		ExpiresAt: now,
		UpdatedAt: now.Add(-30 * time.Minute),
	}
	commitFailure := errors.New("forced expiry commit failure")
	transactions := newAgentExpiryTransactions(stored)
	transactions.commitErr = commitFailure
	service := expiry.NewService(transactions, fixedClock{now: now})

	got, err := service.Expire(context.Background(), expiry.Input{SessionID: stored.ID})
	if !errors.Is(err, commitFailure) {
		t.Fatalf("Expire() error = %v, want forced commit failure", err)
	}
	if got != (expiry.Result{}) {
		t.Errorf("Expire() result = %#v, want empty result", got)
	}
	if transactions.guardCalls != 1 || transactions.appendCalls != 1 {
		t.Errorf("pre-commit calls = guard:%d append:%d, want 1/1", transactions.guardCalls, transactions.appendCalls)
	}
	if transactions.state.Status != stored.Status ||
		!transactions.state.UpdatedAt.Equal(stored.UpdatedAt) ||
		len(transactions.events) != 0 {
		t.Errorf(
			"commit-failure durable effects = status:%q updated:%s events:%d, want unchanged",
			transactions.state.Status,
			transactions.state.UpdatedAt,
			len(transactions.events),
		)
	}
}

func TestExpiryWriteFailureReturnsEmptyResultAndRollsBack(t *testing.T) {
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	stored := session.VerificationSession{
		ID:        uuid.MustParse("c37709a6-b3b8-4b43-9d39-ed8e68513dbc"),
		Status:    verificationsession.Created,
		ExpiresAt: now,
		UpdatedAt: now.Add(-30 * time.Minute),
	}

	tests := []struct {
		name            string
		configure       func(*agentExpiryTransactions, error)
		wantGuardCalls  int
		wantAppendCalls int
	}{
		{
			name: "guarded update failure",
			configure: func(transactions *agentExpiryTransactions, failure error) {
				transactions.guardErr = failure
			},
			wantGuardCalls: 1,
		},
		{
			name: "event append failure",
			configure: func(transactions *agentExpiryTransactions, failure error) {
				transactions.appendErr = failure
			},
			wantGuardCalls:  1,
			wantAppendCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failure := errors.New("forced expiry write failure")
			transactions := newAgentExpiryTransactions(stored)
			tt.configure(transactions, failure)
			service := expiry.NewService(transactions, fixedClock{now: now})

			got, err := service.Expire(context.Background(), expiry.Input{SessionID: stored.ID})
			if !errors.Is(err, failure) {
				t.Fatalf("Expire() error = %v, want forced write failure", err)
			}
			if got != (expiry.Result{}) {
				t.Errorf("Expire() result = %#v, want empty result", got)
			}
			if transactions.guardCalls != tt.wantGuardCalls || transactions.appendCalls != tt.wantAppendCalls {
				t.Errorf(
					"write-failure calls = guard:%d append:%d, want %d/%d",
					transactions.guardCalls,
					transactions.appendCalls,
					tt.wantGuardCalls,
					tt.wantAppendCalls,
				)
			}
			if transactions.state.Status != stored.Status ||
				!transactions.state.UpdatedAt.Equal(stored.UpdatedAt) ||
				len(transactions.events) != 0 {
				t.Errorf(
					"write-failure durable effects = status:%q updated:%s events:%d, want unchanged",
					transactions.state.Status,
					transactions.state.UpdatedAt,
					len(transactions.events),
				)
			}
		})
	}
}

type agentExpiryTransactions struct {
	state       session.VerificationSession
	events      []session.AppendEventParams
	guardResult bool
	guardErr    error
	appendErr   error
	commitErr   error
	guardCalls  int
	appendCalls int
}

func newAgentExpiryTransactions(stored session.VerificationSession) *agentExpiryTransactions {
	return &agentExpiryTransactions{state: stored, guardResult: true}
}

func (m *agentExpiryTransactions) WithinTransaction(
	ctx context.Context,
	operation func(expiry.Transaction) error,
) error {
	working := &agentExpiryTransaction{
		owner:  m,
		state:  m.state,
		events: append([]session.AppendEventParams(nil), m.events...),
	}
	if err := operation(working); err != nil {
		return err
	}
	if m.commitErr != nil {
		return m.commitErr
	}
	m.state = working.state
	m.events = working.events
	return nil
}

type agentExpiryTransaction struct {
	owner  *agentExpiryTransactions
	state  session.VerificationSession
	events []session.AppendEventParams
}

func (m *agentExpiryTransaction) LockSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if sessionID != m.state.ID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.state, nil
}

func (m *agentExpiryTransaction) GuardExpiry(
	_ context.Context,
	sessionID uuid.UUID,
	currentState verificationsession.State,
	expiredAt time.Time,
) (bool, error) {
	m.owner.guardCalls++
	if m.owner.guardErr != nil {
		return false, m.owner.guardErr
	}
	if !m.owner.guardResult || sessionID != m.state.ID || currentState != m.state.Status {
		return false, nil
	}
	m.state.Status = verificationsession.Expired
	m.state.UpdatedAt = expiredAt
	return true, nil
}

func (m *agentExpiryTransaction) AppendEvent(
	_ context.Context,
	event session.AppendEventParams,
) error {
	m.owner.appendCalls++
	if m.owner.appendErr != nil {
		return m.owner.appendErr
	}
	m.events = append(m.events, event)
	return nil
}
