package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/deterministicextractor"
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

func TestPostgresArtifactConfirmPersistsMismatchOutcomeAtomically(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)

	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	service := artifact.NewService(
		postgresArtifacts,
		postgresArtifacts,
		artifactPostgresObjectStorage{metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "mismatch-etag",
		}},
		deterministicextractor.New(
			map[string]artifact.DocumentExtraction{
				"artifact/" + fixture.uploadIntentID.String(): {
					IdentityNumber: "different-identity-number",
				},
			},
			nil,
		),
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	got, err := service.Confirm(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		fixture.uploadIntentID,
	)

	if got != (session.Summary{}) {
		t.Errorf("Confirm() summary = %#v, want zero summary", got)
	}
	var artifactError *artifact.Error
	if !errors.As(err, &artifactError) {
		t.Fatalf("Confirm() error = %v, want artifact.Error", err)
	}
	if artifactError.Code != artifact.CodeLocalValidationFailed ||
		artifactError.Reason != artifact.ReasonIdentityNumberMismatch {
		t.Fatalf(
			"Confirm() error = %s/%s, want %s/%s",
			artifactError.Code,
			artifactError.Reason,
			artifact.CodeLocalValidationFailed,
			artifact.ReasonIdentityNumberMismatch,
		)
	}

	var intentStatus string
	var failureCode string
	var confirmedAt *time.Time
	var latestStatusChangeAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, failure_code, confirmed_at, latest_status_change_at
		FROM upload_intents
		WHERE id = $1
	`, fixture.uploadIntentID).Scan(
		&intentStatus,
		&failureCode,
		&confirmedAt,
		&latestStatusChangeAt,
	); err != nil {
		t.Fatalf("read validation-failed Upload Intent: %v", err)
	}
	if intentStatus != "validation_failed" {
		t.Errorf("Upload Intent status = %q, want validation_failed", intentStatus)
	}
	if failureCode != string(artifact.ReasonIdentityNumberMismatch) {
		t.Errorf(
			"Upload Intent failure_code = %q, want %q",
			failureCode,
			artifact.ReasonIdentityNumberMismatch,
		)
	}
	if confirmedAt != nil {
		t.Errorf("Upload Intent confirmed_at = %v, want nil", confirmedAt)
	}
	if !latestStatusChangeAt.Equal(now) {
		t.Errorf(
			"Upload Intent latest_status_change_at = %s, want %s",
			latestStatusChangeAt,
			now,
		)
	}

	var artifactCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_artifacts
		WHERE upload_intent_id = $1
	`, fixture.uploadIntentID).Scan(&artifactCount); err != nil {
		t.Fatalf("count mismatch Verification Artifacts: %v", err)
	}
	if artifactCount != 0 {
		t.Errorf("Verification Artifact count = %d, want 0", artifactCount)
	}

	var sessionStatus string
	if err := database.QueryRow(ctx, `
		SELECT status
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&sessionStatus); err != nil {
		t.Fatalf("read mismatch Verification Session: %v", err)
	}
	if sessionStatus != session.StatusPersonalDetailsSubmitted.String() {
		t.Errorf(
			"Verification Session status = %q, want %q",
			sessionStatus,
			session.StatusPersonalDetailsSubmitted,
		)
	}

	var eventType string
	var eventOutcome string
	var eventOccurredAt time.Time
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			event_type,
			metadata ->> 'outcome',
			occurred_at,
			count(*) OVER ()
		FROM session_events
		WHERE session_id = $1
	`, fixture.sessionID).Scan(
		&eventType,
		&eventOutcome,
		&eventOccurredAt,
		&eventCount,
	); err != nil {
		t.Fatalf("read mismatch Session Event: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("Session Event count = %d, want 1", eventCount)
	}
	if eventType != sessionevent.ConfirmIdentityDocument.String() {
		t.Errorf(
			"Session Event type = %q, want %q",
			eventType,
			sessionevent.ConfirmIdentityDocument,
		)
	}
	if eventOutcome != string(sessionevent.OutcomeLocalValidationFailed) {
		t.Errorf(
			"Session Event outcome = %q, want %q",
			eventOutcome,
			sessionevent.OutcomeLocalValidationFailed,
		)
	}
	if !eventOccurredAt.Equal(now) {
		t.Errorf("Session Event occurred_at = %s, want %s", eventOccurredAt, now)
	}
}

func TestPostgresArtifactConfirmRollsBackAcceptedOutcomeWhenEventWriteFails(t *testing.T) {
	now := time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)

	if _, err := database.Exec(ctx, `
		CREATE FUNCTION reject_confirm_identity_document_event()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.event_type = 'confirm_identity_document' THEN
				RAISE EXCEPTION 'forced confirm event failure';
			END IF;
			RETURN NEW;
		END;
		$$;

		CREATE TRIGGER reject_confirm_identity_document_event
		BEFORE INSERT ON session_events
		FOR EACH ROW
		EXECUTE FUNCTION reject_confirm_identity_document_event();
	`); err != nil {
		t.Fatalf("install forced event failure: %v", err)
	}

	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	service := artifact.NewService(
		postgresArtifacts,
		postgresArtifacts,
		artifactPostgresObjectStorage{metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "rollback-etag",
		}},
		artifactPostgresExtractor{extraction: artifact.DocumentExtraction{
			IdentityNumber: fixture.identityNumber,
		}},
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	got, err := service.Confirm(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		fixture.uploadIntentID,
	)

	if err == nil {
		t.Fatal("Confirm() error = nil, want forced database error")
	}
	if got != (session.Summary{}) {
		t.Errorf("Confirm() summary = %#v, want zero summary", got)
	}

	var intentStatus string
	var confirmedAt *time.Time
	var failureCode *string
	if err := database.QueryRow(ctx, `
		SELECT status, confirmed_at, failure_code
		FROM upload_intents
		WHERE id = $1
	`, fixture.uploadIntentID).Scan(
		&intentStatus,
		&confirmedAt,
		&failureCode,
	); err != nil {
		t.Fatalf("read rolled-back Upload Intent: %v", err)
	}
	if intentStatus != "pending" {
		t.Errorf("Upload Intent status = %q, want pending", intentStatus)
	}
	if confirmedAt != nil {
		t.Errorf("Upload Intent confirmed_at = %v, want nil", confirmedAt)
	}
	if failureCode != nil {
		t.Errorf("Upload Intent failure_code = %v, want nil", failureCode)
	}

	var artifactCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_artifacts
		WHERE upload_intent_id = $1
	`, fixture.uploadIntentID).Scan(&artifactCount); err != nil {
		t.Fatalf("count rolled-back Verification Artifacts: %v", err)
	}
	if artifactCount != 0 {
		t.Errorf("Verification Artifact count = %d, want 0", artifactCount)
	}

	var sessionStatus string
	if err := database.QueryRow(ctx, `
		SELECT status
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&sessionStatus); err != nil {
		t.Fatalf("read rolled-back Verification Session: %v", err)
	}
	if sessionStatus != session.StatusPersonalDetailsSubmitted.String() {
		t.Errorf(
			"Verification Session status = %q, want %q",
			sessionStatus,
			session.StatusPersonalDetailsSubmitted,
		)
	}

	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM session_events
		WHERE session_id = $1
	`, fixture.sessionID).Scan(&eventCount); err != nil {
		t.Fatalf("count rolled-back Session Events: %v", err)
	}
	if eventCount != 0 {
		t.Errorf("Session Event count = %d, want 0", eventCount)
	}
}

func TestPostgresCreateIdentityUploadIntentAtomicallySupersedesPendingIntent(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	rawToken := "postgres-create-intent-token"
	tokens := session.NewProductionCryptoTokens()

	var sessionID uuid.UUID
	if err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (
			resume_token_hash,
			status,
			expires_at
		)
		VALUES ($1, 'personal_details_submitted', $2)
		RETURNING id
	`, tokens.Hash(rawToken), now.Add(30*time.Minute)).Scan(&sessionID); err != nil {
		t.Fatalf("insert create-intent Verification Session: %v", err)
	}

	oldIntentID := uuid.MustParse("25de40e1-6a1a-416d-9124-b28987c79431")
	oldCreatedAt := now.Add(-time.Minute)
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
		VALUES ($1, $2, 'identity_document', $3, $4, $4, $5)
	`, oldIntentID, sessionID, "verification-sessions/old", oldCreatedAt, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("insert old pending Upload Intent: %v", err)
	}

	presigner := &artifactPostgresPresigner{
		url: "https://uploads.example.test/postgres-signed",
	}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	service := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(ctx, sessionID, rawToken, "identity_document")

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

	rows, err := database.Query(ctx, `
		SELECT
			id,
			status,
			storage_key,
			created_at,
			latest_status_change_at,
			expires_at
		FROM upload_intents
		WHERE verification_session_id = $1
		  AND kind = 'identity_document'
		ORDER BY created_at, id
	`, sessionID)
	if err != nil {
		t.Fatalf("read replacement Upload Intents: %v", err)
	}
	defer rows.Close()

	type storedIntent struct {
		id                   uuid.UUID
		status               string
		storageKey           string
		createdAt            time.Time
		latestStatusChangeAt time.Time
		expiresAt            time.Time
	}
	stored := make([]storedIntent, 0, 2)
	for rows.Next() {
		var intent storedIntent
		if err := rows.Scan(
			&intent.id,
			&intent.status,
			&intent.storageKey,
			&intent.createdAt,
			&intent.latestStatusChangeAt,
			&intent.expiresAt,
		); err != nil {
			t.Fatalf("scan replacement Upload Intent: %v", err)
		}
		stored = append(stored, intent)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate replacement Upload Intents: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored Upload Intent count = %d, want 2", len(stored))
	}

	oldStored := stored[0]
	if oldStored.id != oldIntentID ||
		oldStored.status != "superseded" ||
		oldStored.storageKey != "verification-sessions/old" ||
		!oldStored.createdAt.Equal(oldCreatedAt) ||
		!oldStored.latestStatusChangeAt.Equal(now) {
		t.Errorf("old Upload Intent = %#v, want superseded historical row", oldStored)
	}
	newStored := stored[1]
	if newStored.id != created.ID ||
		newStored.status != "pending" ||
		newStored.storageKey != expectedKey ||
		!newStored.createdAt.Equal(now) ||
		!newStored.latestStatusChangeAt.Equal(now) ||
		!newStored.expiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("new Upload Intent = %#v, want exact pending replacement", newStored)
	}
}

func TestConcurrentPostgresCreateIdentityUploadIntentLeavesOnePendingIntent(t *testing.T) {
	now := time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	rawToken := "concurrent-create-intent-token"
	tokens := session.NewProductionCryptoTokens()

	var sessionID uuid.UUID
	if err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (
			resume_token_hash,
			status,
			expires_at
		)
		VALUES ($1, 'personal_details_submitted', $2)
		RETURNING id
	`, tokens.Hash(rawToken), now.Add(30*time.Minute)).Scan(&sessionID); err != nil {
		t.Fatalf("insert concurrent-create Verification Session: %v", err)
	}

	initialIntentID := uuid.MustParse("1a6d692f-ee5c-4cb8-841d-c74317f319a2")
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			expires_at
		)
		VALUES ($1, $2, 'identity_document', $3, $4)
	`, initialIntentID, sessionID, "verification-sessions/concurrent-old", now.Add(4*time.Minute)); err != nil {
		t.Fatalf("insert concurrent old Upload Intent: %v", err)
	}

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open concurrent artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	presigner := &concurrentArtifactPresigner{}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	start := make(chan struct{})
	results := make(chan artifact.CreatedUploadIntent, 2)
	errs := make(chan error, 2)
	var callers sync.WaitGroup
	callers.Add(2)
	for range 2 {
		go func() {
			defer callers.Done()
			<-start
			created, err := service.Create(
				ctx,
				sessionID,
				rawToken,
				"identity_document",
			)
			results <- created
			errs <- err
		}()
	}
	close(start)
	callers.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Create() error = %v", err)
		}
	}
	createdIDs := make(map[uuid.UUID]struct{}, 2)
	for created := range results {
		if created.ID == uuid.Nil {
			t.Error("concurrent Create() returned nil ID")
		}
		if created.UploadURL == "" {
			t.Error("concurrent Create() returned empty UploadURL")
		}
		createdIDs[created.ID] = struct{}{}
	}
	if len(createdIDs) != 2 {
		t.Errorf("unique created ID count = %d, want 2", len(createdIDs))
	}
	if presigner.uniqueKeyCount() != 2 {
		t.Errorf("unique presigned key count = %d, want 2", presigner.uniqueKeyCount())
	}

	var totalCount int
	var pendingCount int
	var supersededCount int
	var uniqueStorageKeyCount int
	if err := database.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status = 'superseded'),
			count(DISTINCT storage_key)
		FROM upload_intents
		WHERE verification_session_id = $1
		  AND kind = 'identity_document'
	`, sessionID).Scan(
		&totalCount,
		&pendingCount,
		&supersededCount,
		&uniqueStorageKeyCount,
	); err != nil {
		t.Fatalf("read concurrent Upload Intent outcome: %v", err)
	}
	if totalCount != 3 {
		t.Errorf("Upload Intent count = %d, want 3", totalCount)
	}
	if pendingCount != 1 {
		t.Errorf("pending Upload Intent count = %d, want 1", pendingCount)
	}
	if supersededCount != 2 {
		t.Errorf("superseded Upload Intent count = %d, want 2", supersededCount)
	}
	if uniqueStorageKeyCount != 3 {
		t.Errorf("unique storage key count = %d, want 3", uniqueStorageKeyCount)
	}
}

type artifactConfirmationFixture struct {
	sessionID      uuid.UUID
	uploadIntentID uuid.UUID
	rawToken       string
	identityNumber string
}

func seedArtifactConfirmationState(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	now time.Time,
) artifactConfirmationFixture {
	t.Helper()

	rawToken := "artifact-confirmation-token-" + uuid.NewString()
	storedHash := session.NewProductionCryptoTokens().Hash(rawToken)
	var sessionID uuid.UUID
	if err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (
			resume_token_hash,
			status,
			expires_at
		)
		VALUES ($1, 'personal_details_submitted', $2)
		RETURNING id
	`, storedHash, now.Add(30*time.Minute)).Scan(&sessionID); err != nil {
		t.Fatalf("insert fixture Verification Session: %v", err)
	}

	const identityNumber = "127100000000009"
	if _, err := database.Exec(ctx, `
		INSERT INTO personal_details (
			verification_session_id,
			full_name,
			date_of_birth,
			identity_number,
			address
		)
		VALUES ($1, '', DATE '2000-02-29', $2, '')
	`, sessionID, identityNumber); err != nil {
		t.Fatalf("insert fixture Personal Details: %v", err)
	}

	uploadIntentID := uuid.New()
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			expires_at
		)
		VALUES ($1, $2, 'identity_document', $3, $4)
	`, uploadIntentID, sessionID, "artifact/"+uploadIntentID.String(), now.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert fixture Upload Intent: %v", err)
	}

	return artifactConfirmationFixture{
		sessionID:      sessionID,
		uploadIntentID: uploadIntentID,
		rawToken:       rawToken,
		identityNumber: identityNumber,
	}
}

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

type artifactPostgresPresigner struct {
	url        string
	storageKey string
	ttl        time.Duration
	err        error
}

func (p *artifactPostgresPresigner) PresignUpload(
	_ context.Context,
	storageKey string,
	ttl time.Duration,
) (string, error) {
	p.storageKey = storageKey
	p.ttl = ttl
	return p.url, p.err
}

type concurrentArtifactPresigner struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

func (p *concurrentArtifactPresigner) PresignUpload(
	_ context.Context,
	storageKey string,
	_ time.Duration,
) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys == nil {
		p.keys = make(map[string]struct{})
	}
	p.keys[storageKey] = struct{}{}
	return "https://uploads.example.test/" + storageKey, nil
}

func (p *concurrentArtifactPresigner) uniqueKeyCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}
