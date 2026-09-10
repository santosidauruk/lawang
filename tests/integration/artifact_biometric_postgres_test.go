package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
)

// TestBiometricConfirmPersistsPostgresOutcomeAtomically is the Issue 008
// Checkpoint 3 tracer. Keep this as one behavior: one public Biometric Capture
// confirmation persists its complete accepted outcome in PostgreSQL.
func TestBiometricConfirmPersistsPostgresOutcomeAtomically(t *testing.T) {
	// ARRANGE 1 — fixed facts and disposable PostgreSQL
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	tokens := session.NewProductionCryptoTokens()
	rawToken := "biometric-postgres-confirm-token"
	sessionID := uuid.MustParse("6d467fc6-9986-4a25-a5a7-e0da7d26d925")
	identityIntentID := uuid.MustParse("f55e0d35-cfb8-4275-8128-8cbef7614e48")
	identityArtifactID := uuid.MustParse("3a1df3e1-4d82-4d4c-a100-a883633b9304")
	biometricIntentID := uuid.MustParse("f08aeed7-af4a-4525-b17c-61fd10d4f954")
	sessionCreatedAt := now.Add(-10 * time.Minute)
	identityConfirmedAt := now.Add(-5 * time.Minute)
	biometricIntentCreatedAt := now.Add(-time.Minute)
	sessionExpiresAt := now.Add(30 * time.Minute)
	identityIntentExpiresAt := now.Add(20 * time.Minute)
	biometricIntentExpiresAt := now.Add(5 * time.Minute)
	identityKey := fmt.Sprintf(
		"verification-sessions/%s/identity_document/%s",
		sessionID,
		identityIntentID,
	)
	biometricKey := fmt.Sprintf(
		"verification-sessions/%s/biometric_capture/%s",
		sessionID,
		biometricIntentID,
	)

	// ARRANGE 2 — accepted Identity Document prerequisite
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id,
			resume_token_hash,
			status,
			created_at,
			updated_at,
			expires_at
		)
		VALUES ($1, $2, 'identity_document_uploaded', $3, $4, $5)
	`,
		sessionID,
		tokens.Hash(rawToken),
		sessionCreatedAt,
		identityConfirmedAt,
		sessionExpiresAt,
	); err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO personal_details (
			verification_session_id,
			full_name,
			date_of_birth,
			identity_number,
			address,
			created_at
		)
		VALUES ($1, 'Checkpoint Three', DATE '2000-01-01', '3173000000000008', '', $2)
	`, sessionID, sessionCreatedAt); err != nil {
		t.Fatalf("insert immutable Personal Details prerequisite: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_personal_details', $2)
	`, sessionID, sessionCreatedAt); err != nil {
		t.Fatalf("insert Personal Details Session Event prerequisite: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			status,
			created_at,
			latest_status_change_at,
			expires_at,
			confirmed_at
		)
		VALUES ($1, $2, 'identity_document', $3, 'confirmed', $4, $5, $6, $5)
	`,
		identityIntentID,
		sessionID,
		identityKey,
		sessionCreatedAt,
		identityConfirmedAt,
		identityIntentExpiresAt,
	); err != nil {
		t.Fatalf("insert confirmed Identity Document Upload Intent: %v", err)
	}

	const identityContentType = "image/jpeg"
	const identitySizeBytes int64 = 2048
	const identityETag = "identity-document-etag"
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_artifacts (
			id,
			upload_intent_id,
			verification_session_id,
			kind,
			storage_key,
			content_type,
			size_bytes,
			etag,
			created_at
		)
		VALUES ($1, $2, $3, 'identity_document', $4, $5, $6, $7, $8)
	`,
		identityArtifactID,
		identityIntentID,
		sessionID,
		identityKey,
		identityContentType,
		identitySizeBytes,
		identityETag,
		identityConfirmedAt,
	); err != nil {
		t.Fatalf("insert accepted Identity Document Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_identity_document', '{"outcome":"accepted"}', $2)
	`, sessionID, identityConfirmedAt); err != nil {
		t.Fatalf("insert accepted Identity Document Session Event: %v", err)
	}

	// ARRANGE 3 — pending Biometric Capture intent
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			created_at,
			latest_status_change_at,
			expires_at
		)
		VALUES ($1, $2, 'biometric_capture', $3, $4, $4, $5)
	`,
		biometricIntentID,
		sessionID,
		biometricKey,
		biometricIntentCreatedAt,
		biometricIntentExpiresAt,
	); err != nil {
		t.Fatalf("insert pending Biometric Capture Upload Intent: %v", err)
	}

	// ARRANGE 4 — public service with PostgreSQL adapter
	transactionActive := false
	objectStorage := &biometricPostgresObjectStorage{
		t:                 t,
		wantKey:           biometricKey,
		transactionActive: &transactionActive,
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   4096,
			ETag:        "biometric-capture-etag",
		},
	}
	extractor := &biometricPostgresFailFastExtractor{t: t}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(
		newTransactionObservingConfirmAcquireFunc(pool, &transactionActive),
	)
	service := artifact.NewService(
		postgresArtifacts,
		confirmCoordinator,
		objectStorage,
		extractor,
		tokens,
		fixedClock{now: now},
	)

	// ACT
	got, err := service.Confirm(ctx, sessionID, rawToken, biometricIntentID)

	// ASSERT 1 — returned public behavior
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if got.ID != sessionID ||
		got.Status != session.StatusBiometricCaptureUploaded ||
		!got.ExpiresAt.Equal(sessionExpiresAt) {
		t.Errorf(
			"Confirm() summary = %#v, want session %s in %s until %s",
			got,
			sessionID,
			session.StatusBiometricCaptureUploaded,
			sessionExpiresAt,
		)
	}

	// ASSERT 2 — durable atomic outcome
	var biometricIntentStatus string
	var biometricConfirmedAt time.Time
	var biometricLatestStatusChangeAt time.Time
	var storedBiometricIntentExpiresAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, confirmed_at, latest_status_change_at, expires_at
		FROM upload_intents
		WHERE id = $1
	`, biometricIntentID).Scan(
		&biometricIntentStatus,
		&biometricConfirmedAt,
		&biometricLatestStatusChangeAt,
		&storedBiometricIntentExpiresAt,
	); err != nil {
		t.Fatalf("read confirmed Biometric Capture Upload Intent: %v", err)
	}
	if biometricIntentStatus != "confirmed" ||
		!biometricConfirmedAt.Equal(now) ||
		!biometricLatestStatusChangeAt.Equal(now) ||
		!storedBiometricIntentExpiresAt.Equal(biometricIntentExpiresAt) {
		t.Errorf(
			"stored Biometric Capture Upload Intent = status:%q confirmed:%s latest:%s expires:%s",
			biometricIntentStatus,
			biometricConfirmedAt,
			biometricLatestStatusChangeAt,
			storedBiometricIntentExpiresAt,
		)
	}

	var storedIdentityArtifactID uuid.UUID
	var storedIdentityIntentID uuid.UUID
	var storedIdentitySessionID uuid.UUID
	var storedIdentityKind string
	var storedIdentityKey string
	var storedIdentityContentType string
	var storedIdentitySizeBytes int64
	var storedIdentityETag string
	var storedIdentityCreatedAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT
			id,
			upload_intent_id,
			verification_session_id,
			kind,
			storage_key,
			content_type,
			size_bytes,
			etag,
			created_at
		FROM verification_artifacts
		WHERE id = $1
	`, identityArtifactID).Scan(
		&storedIdentityArtifactID,
		&storedIdentityIntentID,
		&storedIdentitySessionID,
		&storedIdentityKind,
		&storedIdentityKey,
		&storedIdentityContentType,
		&storedIdentitySizeBytes,
		&storedIdentityETag,
		&storedIdentityCreatedAt,
	); err != nil {
		t.Fatalf("read accepted Identity Document Verification Artifact: %v", err)
	}
	if storedIdentityArtifactID != identityArtifactID ||
		storedIdentityIntentID != identityIntentID ||
		storedIdentitySessionID != sessionID ||
		storedIdentityKind != "identity_document" ||
		storedIdentityKey != identityKey ||
		storedIdentityContentType != identityContentType ||
		storedIdentitySizeBytes != identitySizeBytes ||
		storedIdentityETag != identityETag ||
		!storedIdentityCreatedAt.Equal(identityConfirmedAt) {
		t.Errorf("stored Identity Document Verification Artifact changed unexpectedly")
	}

	var biometricArtifactID uuid.UUID
	var biometricArtifactIntentID uuid.UUID
	var biometricArtifactSessionID uuid.UUID
	var biometricArtifactKind string
	var biometricArtifactKey string
	var biometricArtifactContentType string
	var biometricArtifactSizeBytes int64
	var biometricArtifactETag string
	var biometricArtifactCreatedAt time.Time
	var biometricArtifactCount int
	if err := database.QueryRow(ctx, `
		SELECT
			id,
			upload_intent_id,
			verification_session_id,
			kind,
			storage_key,
			content_type,
			size_bytes,
			etag,
			created_at,
			count(*) OVER ()
		FROM verification_artifacts
		WHERE verification_session_id = $1
		  AND kind = 'biometric_capture'
	`, sessionID).Scan(
		&biometricArtifactID,
		&biometricArtifactIntentID,
		&biometricArtifactSessionID,
		&biometricArtifactKind,
		&biometricArtifactKey,
		&biometricArtifactContentType,
		&biometricArtifactSizeBytes,
		&biometricArtifactETag,
		&biometricArtifactCreatedAt,
		&biometricArtifactCount,
	); err != nil {
		t.Fatalf("read accepted Biometric Capture Verification Artifact: %v", err)
	}
	if biometricArtifactCount != 1 ||
		biometricArtifactID == uuid.Nil ||
		biometricArtifactIntentID != biometricIntentID ||
		biometricArtifactSessionID != sessionID ||
		biometricArtifactKind != "biometric_capture" ||
		biometricArtifactKey != biometricKey ||
		biometricArtifactContentType != objectStorage.metadata.ContentType ||
		biometricArtifactSizeBytes != objectStorage.metadata.SizeBytes ||
		biometricArtifactETag != objectStorage.metadata.ETag ||
		!biometricArtifactCreatedAt.Equal(now) {
		t.Errorf(
			"stored Biometric Capture Verification Artifact = id:%s intent:%s session:%s kind:%q key:%q content-type:%q size:%d etag:%q created:%s count:%d",
			biometricArtifactID,
			biometricArtifactIntentID,
			biometricArtifactSessionID,
			biometricArtifactKind,
			biometricArtifactKey,
			biometricArtifactContentType,
			biometricArtifactSizeBytes,
			biometricArtifactETag,
			biometricArtifactCreatedAt,
			biometricArtifactCount,
		)
	}

	var storedSessionStatus string
	var storedSessionExpiresAt time.Time
	var storedSessionUpdatedAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, expires_at, updated_at
		FROM verification_sessions
		WHERE id = $1
	`, sessionID).Scan(
		&storedSessionStatus,
		&storedSessionExpiresAt,
		&storedSessionUpdatedAt,
	); err != nil {
		t.Fatalf("read confirmed Verification Session: %v", err)
	}
	if storedSessionStatus != session.StatusBiometricCaptureUploaded.String() ||
		!storedSessionExpiresAt.Equal(sessionExpiresAt) ||
		!storedSessionUpdatedAt.Equal(now) {
		t.Errorf(
			"stored Verification Session = status:%q expires:%s updated:%s",
			storedSessionStatus,
			storedSessionExpiresAt,
			storedSessionUpdatedAt,
		)
	}

	var rawEventType string
	var rawEventMetadata []byte
	var eventOccurredAt time.Time
	var biometricEventCount int
	if err := database.QueryRow(ctx, `
		SELECT event_type, metadata, occurred_at, count(*) OVER ()
		FROM session_events
		WHERE session_id = $1
		  AND event_type = 'confirm_biometric_capture'
	`, sessionID).Scan(
		&rawEventType,
		&rawEventMetadata,
		&eventOccurredAt,
		&biometricEventCount,
	); err != nil {
		t.Fatalf("read accepted Biometric Capture Session Event: %v", err)
	}
	eventType, err := sessionevent.ParseType(rawEventType)
	if err != nil {
		t.Fatalf("parse Biometric Capture Session Event type: %v", err)
	}
	eventMetadata, err := sessionevent.ParseMetadata(rawEventMetadata)
	if err != nil {
		t.Fatalf("parse Biometric Capture Session Event metadata: %v", err)
	}
	if biometricEventCount != 1 ||
		eventType != sessionevent.ConfirmBiometricCapture ||
		eventMetadata.Outcome() != sessionevent.OutcomeAccepted ||
		!eventOccurredAt.Equal(now) {
		t.Errorf(
			"stored Biometric Capture Session Event = count:%d type:%s outcome:%s occurred:%s",
			biometricEventCount,
			eventType,
			eventMetadata.Outcome(),
			eventOccurredAt,
		)
	}

	// ASSERT 3 — external boundary
	if objectStorage.calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", objectStorage.calls)
	}
	if extractor.calls != 0 {
		t.Errorf("Extract() calls = %d, want 0", extractor.calls)
	}
	if transactionActive {
		t.Error("PostgreSQL transaction still active after Confirm() returned")
	}
}

type biometricPostgresObjectStorage struct {
	t                 *testing.T
	wantKey           string
	transactionActive *bool
	metadata          artifact.ObjectMetadata
	calls             int
}

func (s *biometricPostgresObjectStorage) HeadObject(
	_ context.Context,
	storageKey string,
) (artifact.ObjectMetadata, error) {
	s.calls++
	if storageKey != s.wantKey {
		s.t.Errorf("HeadObject() key = %q, want %q", storageKey, s.wantKey)
	}
	if *s.transactionActive {
		s.t.Error("HeadObject() called while PostgreSQL transaction is active")
	}
	return s.metadata, nil
}

type biometricPostgresFailFastExtractor struct {
	t     *testing.T
	calls int
}

func (e *biometricPostgresFailFastExtractor) Extract(
	_ context.Context,
	storageKey string,
) (artifact.DocumentExtraction, error) {
	e.calls++
	e.t.Fatalf("Extract() called for Biometric Capture key %q", storageKey)
	return artifact.DocumentExtraction{}, nil
}

type transactionObservingConnLease struct {
	postgresadapter.ConnLease
	transactionActive *bool
}

func (l *transactionObservingConnLease) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := l.ConnLease.Begin(ctx)
	if err != nil {
		return nil, err
	}
	*l.transactionActive = true
	return &transactionObservingTx{
		Tx:                tx,
		transactionActive: l.transactionActive,
	}, nil
}

type transactionObservingTx struct {
	pgx.Tx
	transactionActive *bool
}

func (tx *transactionObservingTx) Commit(ctx context.Context) error {
	err := tx.Tx.Commit(ctx)
	*tx.transactionActive = false
	return err
}

func (tx *transactionObservingTx) Rollback(ctx context.Context) error {
	err := tx.Tx.Rollback(ctx)
	*tx.transactionActive = false
	return err
}

func newTransactionObservingConfirmAcquireFunc(
	pool *pgxpool.Pool,
	transactionActive *bool,
) postgresadapter.AcquireFunc {
	baseAcquire := newArtifactConfirmAcquireFunc(pool)
	return func(ctx context.Context) (postgresadapter.ConnLease, error) {
		lease, err := baseAcquire(ctx)
		if err != nil {
			return nil, err
		}
		return &transactionObservingConnLease{
			ConnLease:         lease,
			transactionActive: transactionActive,
		}, nil
	}
}
