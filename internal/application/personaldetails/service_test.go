package personaldetails_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/personaldetails"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

func TestSubmitFirstPersonalDetailsCommitsDetailsStateAndOneSafeEvent(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID:              id,
		Status:          session.StatusCreated,
		ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)
	input := personaldetails.Input{
		FullName:       "Alice Applicant",
		DateOfBirth:    time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "1234567890",
		Address:        "Jalan Perjuangan 1",
	}

	got, err := service.Submit(context.Background(), id, "raw-resume-token", input)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	if got.ID != id || got.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("Submit() summary = %#v, want submitted session %s", got, id)
	}
	if transactions.state.session.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("stored state = %q, want %q", transactions.state.session.Status, session.StatusPersonalDetailsSubmitted)
	}
	if transactions.state.details == nil || transactions.state.details.Input != input {
		t.Errorf("stored Personal Details = %#v, want %#v", transactions.state.details, input)
	}
	if len(transactions.state.events) != 1 {
		t.Fatalf("stored events = %d, want exactly 1", len(transactions.state.events))
	}
	event := transactions.state.events[0]
	if event.Type != sessionevent.SubmitPersonalDetails || event.Metadata.Outcome() != "" {
		t.Errorf("stored event = %#v, want safe submit_personal_details with empty metadata", event)
	}
}

func TestSubmitIdenticalPersonalDetailsReturnsCurrentSummaryWithoutAnotherWrite(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusCreated, ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt: now.Add(30 * time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)
	input := personaldetails.Input{
		FullName: "", DateOfBirth: time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "", Address: "",
	}

	first, err := service.Submit(context.Background(), id, "raw-resume-token", input)
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	replayed, err := service.Submit(context.Background(), id, "raw-resume-token", input)
	if err != nil {
		t.Fatalf("replayed Submit() error = %v", err)
	}

	if replayed != first {
		t.Errorf("replayed summary = %#v, want %#v", replayed, first)
	}
	if transactions.state.detailWrites != 1 {
		t.Errorf("Personal Details writes after identical replay = %d, want exactly 1", transactions.state.detailWrites)
	}
	if len(transactions.state.events) != 1 {
		t.Errorf("events after identical replay = %d, want exactly 1", len(transactions.state.events))
	}
}

func TestSubmitDifferentPersonalDetailsReturnsConflictWithoutMutation(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusCreated, ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt: now.Add(30 * time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)
	original := personaldetails.Input{
		FullName: "Alice Applicant", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "1234567890", Address: "Jalan Perjuangan 1",
	}
	if _, err := service.Submit(context.Background(), id, "raw-resume-token", original); err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}

	_, err := service.Submit(context.Background(), id, "raw-resume-token", personaldetails.Input{
		FullName: "Different Applicant", DateOfBirth: original.DateOfBirth,
		IdentityNumber: original.IdentityNumber, Address: original.Address,
	})
	var serviceError *personaldetails.Error
	if !errors.As(err, &serviceError) || serviceError.Code != personaldetails.CodeConflict {
		t.Fatalf("different replay error = %v, want PERSONAL_DETAILS_CONFLICT", err)
	}
	if serviceError.ID != id {
		t.Errorf("conflict id = %s, want %s", serviceError.ID, id)
	}
	if transactions.state.details == nil || transactions.state.details.Input != original {
		t.Errorf("stored Personal Details = %#v, want original %#v", transactions.state.details, original)
	}
	if transactions.state.detailWrites != 1 || len(transactions.state.events) != 1 {
		t.Errorf("conflict committed %d detail writes and %d events, want one each from first submit only", transactions.state.detailWrites, len(transactions.state.events))
	}
}

func TestSubmitRejectsWrongResumeTokenWithoutMutation(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusCreated, ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt: now.Add(30 * time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("wrong-hash"), equal: false},
		fixedClock{now: now},
	)

	_, err := service.Submit(context.Background(), id, "wrong-token", personaldetails.Input{})
	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeInvalidResumeToken {
		t.Fatalf("Submit() error = %v, want INVALID_RESUME_TOKEN", err)
	}
	if transactions.state.detailWrites != 0 || len(transactions.state.events) != 0 {
		t.Errorf("wrong token committed %d detail writes and %d events", transactions.state.detailWrites, len(transactions.state.events))
	}
	if transactions.detailReads != 0 {
		t.Errorf("wrong token caused %d Personal Details reads, want 0", transactions.detailReads)
	}
}

func TestSubmitRejectsExpiredSessionAtDeadlineWithoutMutation(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusCreated, ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt: now,
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Submit(context.Background(), id, "raw-resume-token", personaldetails.Input{})
	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionExpired {
		t.Fatalf("Submit() error = %v, want SESSION_EXPIRED", err)
	}
	if transactions.state.detailWrites != 0 || len(transactions.state.events) != 0 {
		t.Errorf("expired submit committed %d detail writes and %d events", transactions.state.detailWrites, len(transactions.state.events))
	}
	if transactions.detailReads != 0 {
		t.Errorf("expired submit caused %d Personal Details reads, want 0", transactions.detailReads)
	}
}

func TestSubmitReportsUnknownSessionUsingPublicErrorCode(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	storedID := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	requestedID := uuid.MustParse("2baa9a1d-06e3-4872-a3eb-b0f9a26d0c1d")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: storedID, Status: session.StatusCreated, ExpiresAt: now.Add(time.Minute),
	})
	service := personaldetails.NewService(transactions, &stubTokenIssuer{}, fixedClock{now: now})

	_, err := service.Submit(context.Background(), requestedID, "any-token", personaldetails.Input{})
	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionNotFound {
		t.Fatalf("Submit() error = %v, want SESSION_NOT_FOUND", err)
	}
}

func TestSubmitFromWrongStateReturnsIllegalTransitionWithoutMutation(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusPersonalDetailsSubmitted,
		ResumeTokenHash: []byte("stored-hash"), ExpiresAt: now.Add(time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Submit(context.Background(), id, "raw-resume-token", personaldetails.Input{})
	var serviceError *personaldetails.Error
	if !errors.As(err, &serviceError) || serviceError.Code != personaldetails.CodeIllegalTransition {
		t.Fatalf("Submit() error = %v, want ILLEGAL_TRANSITION", err)
	}
	if serviceError.ID != id || serviceError.From != session.StatusPersonalDetailsSubmitted || serviceError.Action != sessionevent.SubmitPersonalDetails {
		t.Errorf("illegal transition error = %#v, want id/from/action context", serviceError)
	}
	if transactions.state.detailWrites != 0 || len(transactions.state.events) != 0 {
		t.Errorf("wrong-state submit committed %d detail writes and %d events", transactions.state.detailWrites, len(transactions.state.events))
	}
}

func TestSubmitFromTerminalStateReturnsIllegalTransitionWithoutMutation(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	transactions := newMemoryTransactions(session.VerificationSession{
		ID: id, Status: session.StatusVerified,
		ResumeTokenHash: []byte("stored-hash"), ExpiresAt: now.Add(time.Minute),
	})
	service := personaldetails.NewService(
		transactions,
		&stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Submit(context.Background(), id, "raw-resume-token", personaldetails.Input{})
	var serviceError *personaldetails.Error
	if !errors.As(err, &serviceError) || serviceError.Code != personaldetails.CodeIllegalTransition {
		t.Fatalf("Submit() error = %v, want ILLEGAL_TRANSITION", err)
	}
	if serviceError.From != session.StatusVerified || transactions.state.detailWrites != 0 || len(transactions.state.events) != 0 {
		t.Errorf("terminal submit error/state = %#v, writes=%d events=%d", serviceError, transactions.state.detailWrites, len(transactions.state.events))
	}
}

type memoryState struct {
	session      session.VerificationSession
	details      *personaldetails.PersonalDetails
	detailWrites int
	events       []session.AppendEventParams
}

type memoryTransactions struct {
	state       memoryState
	detailReads int
}

func newMemoryTransactions(stored session.VerificationSession) *memoryTransactions {
	return &memoryTransactions{state: memoryState{session: stored}}
}

func (m *memoryTransactions) WithinTransaction(
	ctx context.Context,
	operation func(personaldetails.Transaction) error,
) error {
	next := m.state
	if m.state.details != nil {
		copyDetails := *m.state.details
		next.details = &copyDetails
	}
	next.events = append([]session.AppendEventParams(nil), m.state.events...)
	if err := operation(&memoryTransaction{state: &next, detailReads: &m.detailReads}); err != nil {
		return err
	}
	m.state = next
	return nil
}

type memoryTransaction struct {
	state       *memoryState
	detailReads *int
}

func (m *memoryTransaction) LockSession(
	_ context.Context,
	id uuid.UUID,
) (session.VerificationSession, error) {
	if m.state.session.ID != id {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.state.session, nil
}

func (m *memoryTransaction) LoadDetails(
	_ context.Context,
	_ uuid.UUID,
) (*personaldetails.PersonalDetails, error) {
	*m.detailReads++
	return m.state.details, nil
}

func (m *memoryTransaction) InsertDetails(_ context.Context, details personaldetails.PersonalDetails) error {
	m.state.details = &details
	m.state.detailWrites++
	return nil
}

func (m *memoryTransaction) UpdateState(
	_ context.Context,
	_ uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	updatedAt time.Time,
) error {
	if m.state.session.Status != expected {
		return session.ErrSessionTransitionStale
	}
	m.state.session.Status = next
	m.state.session.UpdatedAt = updatedAt
	return nil
}

func (m *memoryTransaction) AppendEvent(_ context.Context, event session.AppendEventParams) error {
	m.state.events = append(m.state.events, event)
	return nil
}

type stubTokenIssuer struct {
	hash  []byte
	equal bool
}

func (i stubTokenIssuer) Issue() (string, []byte, error) { return "", nil, nil }
func (i *stubTokenIssuer) Hash(string) []byte            { return i.hash }
func (i *stubTokenIssuer) Equal(_, _ []byte) bool        { return i.equal }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
