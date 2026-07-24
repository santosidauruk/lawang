package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

// TestPostgresArtifactConfirmPersistsAcceptedOutcomeAtomically is the Checkpoint 3
// tracer bullet. Keep this as one behavior: a successful call through
// artifact.Service.Confirm persists its whole accepted outcome in PostgreSQL.
//
// Complete the numbered sections in order, then remove t.Skip and keep the first
// compile/test failure as the RED result for review. Do not add mismatch, rollback,
// create-intent, replay, concurrency, HTTP, or MinIO behavior to this test.
func TestPostgresArtifactConfirmPersistsAcceptedOutcomeAtomically(t *testing.T) {
	// ARRANGE 1 — fixed facts and disposable PostgreSQL
	// Call openArtifactDatabase. Define a fixed clock, raw resume token, identity
	// number, Upload Intent UUID, storage key, and future session/intent expiries.
	// Use session.NewProductionCryptoTokens().Hash(rawToken) for the stored hash.
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	rawToken := "raw-token"
	uploadIntentID := uuid.MustParse("bd4518e0-45e2-4a42-ac42-8d3b9f247f02")
	identityNumber := "127100000000009"
	kind := "identity_document"
	storageKey := "storage-key"
	sessionExpiresAt := now.Add(30 * time.Minute)
	intentExpiresAt := now.Add(5 * time.Minute)
	storedHash := session.NewProductionCryptoTokens().Hash(rawToken)

	// ARRANGE 2 — pre-confirmation database state
	// Insert exactly one Verification Session in personal_details_submitted, its
	// immutable Personal Details, and one pending identity_document Upload Intent.
	// Keep setup explicit enough that each stored guard can be read from this test.
	// There must be no Verification Artifact before ACT.
	var sessionID uuid.UUID
	err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (resume_token_hash, status, expires_at)
		VALUES ($1, 'personal_details_submitted', $2)
		RETURNING id
	`, storedHash, sessionExpiresAt).Scan(&sessionID)
	if err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	var dateOfBirth time.Time
	var fullName, address string
	err = database.QueryRow(ctx, `
		INSERT INTO personal_details (
			verification_session_id, full_name, date_of_birth, identity_number, address
		) VALUES ($1, '', DATE '2000-02-29', $2, '')
		RETURNING full_name, date_of_birth, identity_number, address
	`, sessionID, identityNumber).Scan(&fullName, &dateOfBirth, &identityNumber, &address)
	if err != nil {
		t.Fatalf("insert Personal Details: %v", err)
	}

	var initialUploadIntentStatus string
	err = database.QueryRow(ctx, `
		INSERT INTO upload_intents (id, verification_session_id, kind, storage_key, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING status
	`, uploadIntentID, sessionID, kind, storageKey, intentExpiresAt).Scan(&initialUploadIntentStatus)
	if err != nil {
		t.Fatalf("insert Upload Intents: %v", err)
	}
	if initialUploadIntentStatus != "pending" {
		t.Fatalf("initial Upload Intent status = %q, want pending", initialUploadIntentStatus)
	}
	// ARRANGE 3 — external boundaries
	// Use artifactPostgresObjectStorage and artifactPostgresExtractor below. Return
	// a non-empty JPEG no larger than 10 MiB, a stable ETag, and the same identity
	// number as Personal Details. These are the only fakes in this tracer bullet;
	// PostgreSQL itself must be real.
	objectStorage := artifactPostgresObjectStorage{
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "image-etag",
		},
	}

	extraction := artifactPostgresExtractor{
		extraction: artifact.DocumentExtraction{
			IdentityNumber: identityNumber,
		},
	}

	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	tokens := session.NewProductionCryptoTokens()

	// ARRANGE 4 — PostgreSQL adapter and public service
	// Let this test choose the smallest useful constructor for an adapter that
	// satisfies artifact.Reader and artifact.Transactor. Pass that adapter, the two
	// external fakes, production token hashing, and the fixed clock to
	// artifact.NewService. Do not expose pgx or generated sqlc types from the adapter.
	service := artifact.NewService(
		postgresArtifacts,
		postgresArtifacts,
		objectStorage,
		extraction,
		tokens,
		fixedClock{now: now},
	)

	// ACT
	// Call service.Confirm once through the public application interface.
	got, err := service.Confirm(ctx, sessionID, rawToken, uploadIntentID)

	// ASSERT — returned behavior and committed outcome
	// First assert the returned session.Summary is identity_document_uploaded.
	// Then prove PostgreSQL contains one atomic accepted outcome:
	//   1. the Upload Intent is confirmed with the fixed confirmation time;
	//   2. exactly one accepted Verification Artifact contains the expected bounded
	//      kind, storage key, content type, size, and ETag;
	//   3. the Verification Session is identity_document_uploaded; and
	//   4. exactly one confirm_identity_document event has outcome accepted.
	// Querying committed rows is allowed here because persistence is the boundary
	// under test. Never assert private adapter calls or generated sqlc row shapes.
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	if got.Status != session.StatusIdentityDocumentUploaded {
		t.Errorf("session summary status got = %s, want %s", got.Status, session.StatusIdentityDocumentUploaded)
	}

	if got.ID != sessionID {
		t.Errorf("session summary id got = %v, want %v", got.ID, sessionID)
	}

	if !got.ExpiresAt.Equal(sessionExpiresAt) {
		t.Errorf("session expiresAt got = %v, want %v", got.ExpiresAt, sessionExpiresAt)
	}

	var uploadIntentStatus string
	var uploadIntentConfirmedAt, uploadIntentLatestStatusChangeAt time.Time
	var storedIntentExpiresAt time.Time
	var failureCode *string
	var objectDeletedAt *time.Time
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			confirmed_at,
			latest_status_change_at,
			expires_at,
			failure_code,
			object_deleted_at
		FROM upload_intents
		WHERE id = $1
	`, uploadIntentID).Scan(
		&uploadIntentStatus,
		&uploadIntentConfirmedAt,
		&uploadIntentLatestStatusChangeAt,
		&storedIntentExpiresAt,
		&failureCode,
		&objectDeletedAt,
	); err != nil {
		t.Fatalf("read confirmed Upload Intent: %v", err)
	}

	if uploadIntentStatus != "confirmed" {
		t.Errorf("upload intent status = %s, want %s", uploadIntentStatus, "confirmed")
	}

	if !uploadIntentLatestStatusChangeAt.Equal(now) {
		t.Errorf("upload intent latestStatusChangeAt = %s, want %s", uploadIntentLatestStatusChangeAt, now)
	}

	if !uploadIntentConfirmedAt.Equal(now) {
		t.Errorf("upload intent confirmed_at = %s, want %s", uploadIntentConfirmedAt, now)
	}

	if !storedIntentExpiresAt.Equal(intentExpiresAt) {
		t.Errorf("upload intent expires_at = %s, want unchanged %s", storedIntentExpiresAt, intentExpiresAt)
	}

	if failureCode != nil {
		t.Errorf("upload intent failure_code = %v, want nil", failureCode)
	}

	if objectDeletedAt != nil {
		t.Errorf("upload intent object_deleted_at = %v, want nil", objectDeletedAt)
	}

	var artifactID, artifactSessionID, artifactUploadIntentID uuid.UUID
	var artifactKind, artifactStorageKey, artifactContentType, artifactETag string
	var artifactSizeBytes int64
	var artifactCreatedAt time.Time
	var artifactCount int
	if err := database.QueryRow(ctx, `
		SELECT
			id,
			verification_session_id,
			upload_intent_id,
			kind,
			storage_key,
			content_type,
			size_bytes,
			etag,
			created_at,
			count(*) OVER ()
		FROM verification_artifacts
		WHERE verification_session_id = $1
		  AND kind = $2
	`, sessionID, kind).Scan(
		&artifactID,
		&artifactSessionID,
		&artifactUploadIntentID,
		&artifactKind,
		&artifactStorageKey,
		&artifactContentType,
		&artifactSizeBytes,
		&artifactETag,
		&artifactCreatedAt,
		&artifactCount,
	); err != nil {
		t.Fatalf("read accepted Verification Artifact: %v", err)
	}

	if artifactCount != 1 {
		t.Errorf("Verification Artifact count = %d, want 1", artifactCount)
	}
	if artifactID == uuid.Nil {
		t.Error("Verification Artifact ID is nil")
	}
	if artifactSessionID != sessionID || artifactUploadIntentID != uploadIntentID {
		t.Errorf(
			"Verification Artifact references session/intent = %s/%s, want %s/%s",
			artifactSessionID,
			artifactUploadIntentID,
			sessionID,
			uploadIntentID,
		)
	}
	if artifactKind != kind ||
		artifactStorageKey != storageKey ||
		artifactContentType != objectStorage.metadata.ContentType ||
		artifactSizeBytes != objectStorage.metadata.SizeBytes ||
		artifactETag != objectStorage.metadata.ETag {
		t.Errorf(
			"Verification Artifact metadata = %q/%q/%q/%d/%q, want %q/%q/%q/%d/%q",
			artifactKind,
			artifactStorageKey,
			artifactContentType,
			artifactSizeBytes,
			artifactETag,
			kind,
			storageKey,
			objectStorage.metadata.ContentType,
			objectStorage.metadata.SizeBytes,
			objectStorage.metadata.ETag,
		)
	}
	if !artifactCreatedAt.Equal(now) {
		t.Errorf("Verification Artifact created_at = %s, want %s", artifactCreatedAt, now)
	}

	var storedSessionStatus string
	var storedSessionExpiresAt, storedSessionUpdatedAt time.Time
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
	if storedSessionStatus != session.StatusIdentityDocumentUploaded.String() {
		t.Errorf(
			"stored Verification Session status = %q, want %q",
			storedSessionStatus,
			session.StatusIdentityDocumentUploaded,
		)
	}
	if !storedSessionExpiresAt.Equal(sessionExpiresAt) {
		t.Errorf(
			"stored Verification Session expires_at = %s, want unchanged %s",
			storedSessionExpiresAt,
			sessionExpiresAt,
		)
	}
	if !storedSessionUpdatedAt.Equal(now) {
		t.Errorf("stored Verification Session updated_at = %s, want %s", storedSessionUpdatedAt, now)
	}

	var rawEventType string
	var rawMetadata []byte
	var occurredAt time.Time
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT event_type, metadata, occurred_at, count(*) OVER ()
		FROM session_events
		WHERE session_id = $1
	`, sessionID).Scan(
		&rawEventType,
		&rawMetadata,
		&occurredAt,
		&eventCount,
	); err != nil {
		t.Fatalf("read confirm_identity_document Session Event: %v", err)
	}

	if eventCount != 1 {
		t.Errorf("Session Event count = %d, want 1", eventCount)
	}

	eventType, err := sessionevent.ParseType(rawEventType)
	if err != nil {
		t.Fatalf("parse Session Event type: %v", err)
	}

	if eventType != sessionevent.ConfirmIdentityDocument {
		t.Errorf("event type got = %s, want %s", eventType, sessionevent.ConfirmIdentityDocument)
	}

	metadata, err := sessionevent.ParseMetadata(rawMetadata)
	if err != nil {
		t.Fatalf("parse Session Event metadata: %v", err)
	}
	outcome := metadata.Outcome()
	if outcome != sessionevent.OutcomeAccepted {
		t.Errorf("event metadata got = %s, want %s", outcome, sessionevent.OutcomeAccepted)
	}
	if !occurredAt.Equal(now) {
		t.Errorf("event occurred_at got = %s, want %s", occurredAt, now)
	}
}

// openArtifactDatabase deliberately applies migrations only through Upload Intent.
// After the user-authored Verification Artifact migration is reviewed, add 00006 to
// this setup before connecting. Until then, the missing persistence surface is part
// of the expected Checkpoint 3 RED.
func openArtifactDatabase(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	ctx, container, databaseURL := openUploadIntentDatabase(t)

	runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/migrations/00006_create_verification_artifacts.sql",
		"/tmp/00006.sql",
	)

	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect artifact PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })
	return ctx, database
}

type artifactPostgresObjectStorage struct {
	metadata artifact.ObjectMetadata
	err      error
}

func (s artifactPostgresObjectStorage) HeadObject(
	context.Context,
	string,
) (artifact.ObjectMetadata, error) {
	return s.metadata, s.err
}

type artifactPostgresExtractor struct {
	extraction artifact.DocumentExtraction
	err        error
}

func (s artifactPostgresExtractor) Extract(
	context.Context,
	string,
) (artifact.DocumentExtraction, error) {
	return s.extraction, s.err
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}
