package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestCreateReturnsRawTokenWhilePersistingOnlyItsHash(t *testing.T) {
	createdAt := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	storedID := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	store := &recordingStore{
		created: session.VerificationSession{
			ID:        storedID,
			Status:    session.StatusCreated,
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
		},
	}
	issuer := stubTokenIssuer{
		raw:  "raw-resume-token",
		hash: []byte("deterministic-token-hash"),
	}
	service := session.NewService(store, &issuer, fixedClock{now: createdAt})

	got, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if got.ID != storedID || got.Status != session.StatusCreated {
		t.Errorf("Create() session = %#v, want database-created id and status", got)
	}
	if got.ResumeToken != issuer.raw {
		t.Errorf("Create() resume token = %q, want raw issued token", got.ResumeToken)
	}
	if got.ExpiresAt != createdAt.Add(30*time.Minute) {
		t.Errorf("Create() expiry = %s, want %s", got.ExpiresAt, createdAt.Add(30*time.Minute))
	}
	if string(store.createParams.ResumeTokenHash) != string(issuer.hash) {
		t.Errorf("persisted token hash = %q, want %q", store.createParams.ResumeTokenHash, issuer.hash)
	}
	if string(store.createParams.ResumeTokenHash) == got.ResumeToken {
		t.Fatal("storage received the raw resume token")
	}
}

func TestResumeReturnsCurrentSessionForMatchingCredential(t *testing.T) {
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	store := &recordingStore{found: session.VerificationSession{
		ID:              id,
		Status:          session.StatusCreated,
		ResumeTokenHash: []byte("stored-hash"),
		ExpiresAt:       now.Add(time.Minute),
	}}
	issuer := stubTokenIssuer{hash: []byte("stored-hash"), equal: true}
	service := session.NewService(store, &issuer, fixedClock{now: now})

	got, err := service.Resume(context.Background(), id, "raw-resume-token")
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if got.ID != id || got.Status != session.StatusCreated || got.ExpiresAt != store.found.ExpiresAt {
		t.Errorf("Resume() = %#v, want current persisted summary", got)
	}
	if issuer.hashedRaw != "raw-resume-token" {
		t.Errorf("Resume() hashed credential %q, want supplied raw token", issuer.hashedRaw)
	}
}

func TestResumeRejectsWrongCredentialWithPublicErrorCode(t *testing.T) {
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	store := &recordingStore{found: session.VerificationSession{
		ID: id, ResumeTokenHash: []byte("stored-hash"), ExpiresAt: now.Add(time.Minute),
	}}
	issuer := stubTokenIssuer{hash: []byte("wrong-hash"), equal: false}
	service := session.NewService(store, &issuer, fixedClock{now: now})

	_, err := service.Resume(context.Background(), id, "wrong-token")
	var serviceError *session.Error
	if !errors.As(err, &serviceError) {
		t.Fatalf("Resume() error = %v, want typed public error", err)
	}
	if serviceError.Code != session.CodeInvalidResumeToken {
		t.Errorf("Resume() error code = %q, want %q", serviceError.Code, session.CodeInvalidResumeToken)
	}
}

func TestResumeReportsUnknownSessionUsingLegacyPublicErrorCode(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	store := &recordingStore{findErr: session.ErrSessionNotFound}
	issuer := stubTokenIssuer{}
	service := session.NewService(store, &issuer, fixedClock{})

	_, err := service.Resume(context.Background(), id, "any-token")
	var serviceError *session.Error
	if !errors.As(err, &serviceError) {
		t.Fatalf("Resume() error = %v, want typed public error", err)
	}
	if serviceError.Code != session.CodeSessionNotFound {
		t.Errorf("Resume() error code = %q, want %q", serviceError.Code, session.CodeSessionNotFound)
	}
}

func TestResumeRejectsExpiredSessionUsingInjectedClock(t *testing.T) {
	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	store := &recordingStore{found: session.VerificationSession{
		ID: id, ResumeTokenHash: []byte("stored-hash"), ExpiresAt: now,
	}}
	issuer := stubTokenIssuer{hash: []byte("stored-hash"), equal: true}
	service := session.NewService(store, &issuer, fixedClock{now: now})

	_, err := service.Resume(context.Background(), id, "matching-token")
	var serviceError *session.Error
	if !errors.As(err, &serviceError) {
		t.Fatalf("Resume() error = %v, want typed public error", err)
	}
	if serviceError.Code != session.CodeSessionExpired {
		t.Errorf("Resume() error code = %q, want %q", serviceError.Code, session.CodeSessionExpired)
	}
}

func TestCreatePropagatesCancellationToStorage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := cancelAwareStore{}
	issuer := stubTokenIssuer{raw: "raw-resume-token", hash: []byte("token-hash")}
	service := session.NewService(store, &issuer, fixedClock{})

	_, err := service.Create(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() error = %v, want context cancellation from storage", err)
	}
}

type recordingStore struct {
	createParams session.CreateParams
	created      session.VerificationSession
	found        session.VerificationSession
	findErr      error
}

func (s *recordingStore) Create(_ context.Context, params session.CreateParams) (session.VerificationSession, error) {
	s.createParams = params
	s.created.ExpiresAt = params.ExpiresAt
	return s.created, nil
}

func (s *recordingStore) FindByID(_ context.Context, _ uuid.UUID) (session.VerificationSession, error) {
	return s.found, s.findErr
}

type stubTokenIssuer struct {
	raw       string
	hash      []byte
	equal     bool
	hashedRaw string
}

func (i stubTokenIssuer) Issue() (string, []byte, error) { return i.raw, i.hash, nil }
func (i *stubTokenIssuer) Hash(raw string) []byte {
	i.hashedRaw = raw
	return i.hash
}
func (i *stubTokenIssuer) Equal(_, _ []byte) bool { return i.equal }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type cancelAwareStore struct{}

func (cancelAwareStore) Create(ctx context.Context, _ session.CreateParams) (session.VerificationSession, error) {
	return session.VerificationSession{}, ctx.Err()
}

func (cancelAwareStore) FindByID(ctx context.Context, _ uuid.UUID) (session.VerificationSession, error) {
	return session.VerificationSession{}, ctx.Err()
}
