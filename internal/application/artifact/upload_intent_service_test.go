package artifact_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestCreateIdentityUploadIntentPresignsBeforeAtomicReplacement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("62251b18-76f7-410f-8b5f-f72f02eac498")
	oldIntentID := uuid.MustParse("a69337ef-18c2-40c8-bda6-b12dac2175dd")
	rawToken := "create-upload-intent-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)

	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: &artifact.UploadIntent{
			ID:                    oldIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "verification-sessions/old",
			Status:                "pending",
			CreatedAt:             now.Add(-time.Minute),
			LatestStatusChangeAt:  now.Add(-time.Minute),
			ExpiresAt:             now.Add(4 * time.Minute),
		},
		sequence: &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/signed",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		ctx,
		sessionID,
		rawToken,
		"identity_document",
	)

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create() ID is nil")
	}
	if created.UploadURL != presigner.url {
		t.Errorf("Create() UploadURL = %q, want %q", created.UploadURL, presigner.url)
	}
	expectedKey := "verification-sessions/" + sessionID.String() +
		"/identity_document/" + created.ID.String()
	if presigner.storageKey != expectedKey {
		t.Errorf("presigned storage key = %q, want %q", presigner.storageKey, expectedKey)
	}
	if presigner.ttl != 5*time.Minute {
		t.Errorf("presign TTL = %s, want 5m", presigner.ttl)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}

	if transactions.supersededIntent == nil {
		t.Fatal("superseded Upload Intent is nil")
	}
	if transactions.supersededIntent.ID != oldIntentID ||
		transactions.supersededIntent.Status != "superseded" ||
		!transactions.supersededIntent.LatestStatusChangeAt.Equal(now) {
		t.Errorf(
			"superseded Upload Intent = %#v, want old ID/status superseded/time now",
			transactions.supersededIntent,
		)
	}
	if transactions.insertedIntent == nil {
		t.Fatal("inserted Upload Intent is nil")
	}
	inserted := transactions.insertedIntent
	if inserted.ID != created.ID ||
		inserted.VerificationSessionID != sessionID ||
		inserted.Kind != "identity_document" ||
		inserted.StorageKey != expectedKey ||
		inserted.Status != "pending" ||
		!inserted.CreatedAt.Equal(now) ||
		!inserted.LatestStatusChangeAt.Equal(now) ||
		!inserted.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("inserted Upload Intent = %#v, want exact new pending intent", inserted)
	}
}

func TestCreateIdentityUploadIntentPresignFailureWritesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("67571efe-af5a-4cc2-85c4-a85f24b7c92e")
	rawToken := "presign-failure-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 1)
	pending := &artifact.UploadIntent{
		ID:                    uuid.MustParse("25ca5d02-1ce5-4a7e-826a-c8a7f87ef249"),
		VerificationSessionID: sessionID,
		Kind:                  "identity_document",
		Status:                "pending",
	}
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: pending,
		sequence:      &sequence,
	}
	presigner := &recordingUploadPresigner{
		err:      errors.New("presign unavailable"),
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(ctx, sessionID, rawToken, "identity_document")

	requireArtifactError(t, err, artifact.CodeObjectStorageFailed, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 1 || sequence[0] != "presign" {
		t.Errorf("operation sequence = %#v, want [presign]", sequence)
	}
	if transactions.supersededIntent != nil || transactions.insertedIntent != nil {
		t.Errorf(
			"database writes = superseded %#v / inserted %#v, want none",
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
	if transactions.pendingIntent != pending {
		t.Errorf("pending Upload Intent changed after presign failure")
	}
}

func TestCreateIdentityUploadIntentStaleRereadDiscardsURLAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("71642919-3f18-4edb-babc-fe8d86c114d6")
	rawToken := "stale-create-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)
	storedSession := session.VerificationSession{
		ID:              sessionID,
		Status:          session.StatusPersonalDetailsSubmitted,
		ResumeTokenHash: tokens.Hash(rawToken),
		ExpiresAt:       now.Add(30 * time.Minute),
	}
	staleSession := storedSession
	staleSession.Status = session.StatusIdentityDocumentUploaded
	transactions := &memoryUploadIntentTransactions{
		storedSession:         storedSession,
		lockedSessionOverride: &staleSession,
		sequence:              &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/discarded",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(ctx, sessionID, rawToken, "identity_document")

	requireArtifactError(t, err, artifact.CodeUploadIntentStale, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result with no returned URL", created)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}
	if transactions.supersededIntent != nil || transactions.insertedIntent != nil {
		t.Errorf(
			"database writes = superseded %#v / inserted %#v, want none",
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
}

func TestCreateIdentityUploadIntentMapsMissingSession(t *testing.T) {
	sessionID := uuid.MustParse("9091d98f-6413-4e1f-9074-b491f511082c")
	sequence := make([]string, 0)
	transactions := &memoryUploadIntentTransactions{sequence: &sequence}
	presigner := &recordingUploadPresigner{
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		"missing-session-token",
		"identity_document",
	)

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionNotFound {
		t.Fatalf("Create() error = %#v, want %s", err, session.CodeSessionNotFound)
	}
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 0 {
		t.Errorf("operation sequence = %#v, want no presign or transaction", sequence)
	}
}

type recordingUploadPresigner struct {
	url        string
	err        error
	storageKey string
	ttl        time.Duration
	sequence   *[]string
	active     *bool
}

func (p *recordingUploadPresigner) PresignUpload(
	_ context.Context,
	storageKey string,
	ttl time.Duration,
) (string, error) {
	if *p.active {
		panic("presign called inside database transaction")
	}
	*p.sequence = append(*p.sequence, "presign")
	p.storageKey = storageKey
	p.ttl = ttl
	return p.url, p.err
}

type memoryUploadIntentTransactions struct {
	storedSession         session.VerificationSession
	lockedSessionOverride *session.VerificationSession
	pendingIntent         *artifact.UploadIntent
	supersededIntent      *artifact.UploadIntent
	insertedIntent        *artifact.UploadIntent
	sequence              *[]string
	transactionActive     bool
}

func (m *memoryUploadIntentTransactions) LoadSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if m.storedSession.ID != sessionID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.storedSession, nil
}

func (m *memoryUploadIntentTransactions) WithinUploadIntentTransaction(
	_ context.Context,
	operation func(artifact.UploadIntentTransaction) error,
) error {
	*m.sequence = append(*m.sequence, "transaction")
	m.transactionActive = true
	defer func() { m.transactionActive = false }()
	return operation(m)
}

func (m *memoryUploadIntentTransactions) LockSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if m.storedSession.ID != sessionID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	if m.lockedSessionOverride != nil {
		return *m.lockedSessionOverride, nil
	}
	return m.storedSession, nil
}

func (m *memoryUploadIntentTransactions) SupersedePendingUploadIntent(
	_ context.Context,
	sessionID uuid.UUID,
	kind string,
	supersededAt time.Time,
) error {
	if m.pendingIntent == nil ||
		m.pendingIntent.VerificationSessionID != sessionID ||
		m.pendingIntent.Kind != kind ||
		m.pendingIntent.Status != "pending" {
		return nil
	}
	superseded := *m.pendingIntent
	superseded.Status = "superseded"
	superseded.LatestStatusChangeAt = supersededAt
	m.supersededIntent = &superseded
	m.pendingIntent = nil
	return nil
}

func (m *memoryUploadIntentTransactions) InsertUploadIntent(
	_ context.Context,
	intent artifact.UploadIntent,
) error {
	inserted := intent
	m.insertedIntent = &inserted
	m.pendingIntent = &inserted
	return nil
}
