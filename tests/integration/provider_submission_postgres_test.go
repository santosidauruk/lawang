package integration_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

// TestProviderSubmissionSuccessFixtureReachesBiometricCaptureUploaded is the
// user-owned fixture gate for Issue 009 Checkpoint 1, Bagian user step 1.
//
// Keep this test about fixture history only. It must not submit the session, write
// an outbox row, or anticipate the production transaction operation.
func TestProviderSubmissionSuccessFixtureReachesBiometricCaptureUploaded(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()

	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)
	assertProviderSubmissionSuccessFixture(t, ctx, database, fixture)
}

// TestFirstProviderSubmissionCommitsPendingEventAndOutboxAtomically is the
// user-owned success tracer for Issue 009 Checkpoint 1.
//
// Keep this as one behavior: the first eligible submission commits the pending
// state, provider deadline, submit_session event, and unpublished provider:submit
// outbox row as one PostgreSQL unit. Rollback proof remains in the SQL proof and a
// later focused PostgreSQL case; do not add Redis, provider I/O, or an HTTP route.
func TestFirstProviderSubmissionCommitsPendingEventAndOutboxAtomically(t *testing.T) {
	// ARRANGE — reuse the fixture below, then construct the user-authored PostgreSQL
	// transaction operation with a fixed transaction time and application-generated
	// outbox UUID.
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	providerSubmissionTx := postgresadapter.NewProviderSubmissionTransactions(database)

	// ACT — invoke the first-submission operation once.
	//
	err := providerSubmissionTx.WithinTransaction(ctx, func(tx providersubmission.Transaction) error {
		_, err := tx.LockSession(ctx, fixture.sessionID)
		if err != nil {
			return err
		}

		hasRequiredArtifacts, err := tx.HasRequiredAcceptedArtifacts(ctx, fixture.sessionID)
		if err != nil {
			return err
		}
		if !hasRequiredArtifacts {
			return errors.New("artifacts don't have required artifacts")
		}

		err = tx.SetPendingVerification(ctx, fixture.now, fixture.sessionID)
		if err != nil {
			return err
		}

		err = tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID:  fixture.sessionID,
			Type:       sessionevent.SubmitSession,
			OccurredAt: fixture.now,
		})
		if err != nil {
			return err
		}

		return tx.InsertUnpublishedOutbox(ctx, fixture.outboxID, fixture.sessionID)
	})
	if err != nil {
		t.Fatalf("WithinTransaction error: %v", err)
	}

	// ASSERT — require exact verification_pending state, transaction time + 24 hours
	// deadline, one safe submit_session event, and one unpublished provider:submit
	// outbox row whose identifier-only payload refers to this session.
	var sessionStatus string
	var sessionUpdatedAt, sessionExpiresAt, verificationDeadlineAt time.Time
	if err := database.QueryRow(ctx, `
	  SELECT status, updated_at, verification_deadline_at, expires_at
		FROM verification_sessions
		WHERE id = $1 and status = 'verification_pending'
	`, fixture.sessionID).Scan(
		&sessionStatus,
		&sessionUpdatedAt,
		&verificationDeadlineAt,
		&sessionExpiresAt,
	); err != nil {
		t.Fatalf("read verification pending session: %v", err)
	}

	if sessionStatus != session.StatusVerificationPending.String() ||
		!sessionUpdatedAt.Equal(fixture.now) ||
		!verificationDeadlineAt.Equal(fixture.now.Add(24*time.Hour)) ||
		!sessionExpiresAt.Equal(fixture.applicantExpiresAt) {
		t.Errorf("session status: %q, updated_at: %v, verification_deadline_at: %v, expires_at: %v", sessionStatus, sessionUpdatedAt, verificationDeadlineAt, sessionExpiresAt)
	}

	var submitEventCount int
	var submitEventIsExact bool
	if err := database.QueryRow(ctx, `
		SELECT
			count(*),
			COALESCE(bool_and(metadata = '{}'::jsonb AND occurred_at = $2), false)
		FROM session_events
		WHERE session_id = $1
		  AND event_type = 'submit_session'
	`, fixture.sessionID, fixture.now).Scan(
		&submitEventCount,
		&submitEventIsExact,
	); err != nil {
		t.Fatalf("read submit_session event: %v", err)
	}
	if submitEventCount != 1 || !submitEventIsExact {
		t.Errorf(
			"submit_session events = count:%d exact-metadata-and-time:%t, want count:1 exact-metadata-and-time:true",
			submitEventCount,
			submitEventIsExact,
		)
	}

	var outboxSessionID uuid.UUID
	var outboxTaskType string
	var payloadMatches bool
	var publishedAtIsNull bool
	var claimTokenIsNull bool
	var claimedUntilIsNull bool
	var attemptCount int
	var lastErrorCodeIsNull bool
	var providerSubmitRowsForSession int
	if err := database.QueryRow(ctx, `
		SELECT
			verification_session_id,
			task_type,
			payload = jsonb_build_object('sessionId', $2::uuid),
			published_at IS NULL,
			claim_token IS NULL,
			claimed_until IS NULL,
			attempt_count,
			last_error_code IS NULL,
			(
				SELECT count(*)
				FROM outbox
				WHERE verification_session_id = $2
				  AND task_type = 'provider:submit'
			)
		FROM outbox
		WHERE id = $1
	`, fixture.outboxID, fixture.sessionID).Scan(
		&outboxSessionID,
		&outboxTaskType,
		&payloadMatches,
		&publishedAtIsNull,
		&claimTokenIsNull,
		&claimedUntilIsNull,
		&attemptCount,
		&lastErrorCodeIsNull,
		&providerSubmitRowsForSession,
	); err != nil {
		t.Fatalf("read unpublished provider submission outbox: %v", err)
	}
	if outboxSessionID != fixture.sessionID ||
		outboxTaskType != "provider:submit" ||
		!payloadMatches ||
		!publishedAtIsNull ||
		!claimTokenIsNull ||
		!claimedUntilIsNull ||
		attemptCount != 0 ||
		!lastErrorCodeIsNull ||
		providerSubmitRowsForSession != 1 {
		t.Errorf(
			"outbox = session:%s task:%q payload-match:%t unpublished:%t claim-token-null:%t claimed-until-null:%t attempts:%d last-error-null:%t session-task-rows:%d",
			outboxSessionID,
			outboxTaskType,
			payloadMatches,
			publishedAtIsNull,
			claimTokenIsNull,
			claimedUntilIsNull,
			attemptCount,
			lastErrorCodeIsNull,
			providerSubmitRowsForSession,
		)
	}
}

type providerSubmissionSuccessFixture struct {
	now                        time.Time
	sessionID                  uuid.UUID
	sessionCreatedAt           time.Time
	rawToken                   string
	personalDetailsSubmittedAt time.Time
	identityIntentID           uuid.UUID
	identityArtifactID         uuid.UUID
	identityIntentCreatedAt    time.Time
	identityConfirmedAt        time.Time
	biometricIntentID          uuid.UUID
	biometricArtifactID        uuid.UUID
	biometricIntentCreatedAt   time.Time
	biometricConfirmedAt       time.Time
	applicantExpiresAt         time.Time
	outboxID                   uuid.UUID
}

func newProviderSubmissionSuccessFixture() providerSubmissionSuccessFixture {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	return providerSubmissionSuccessFixture{
		now:                        now,
		sessionID:                  uuid.MustParse("c83c63a9-7a19-4af5-8475-93a81af050ea"),
		sessionCreatedAt:           now.Add(-35 * time.Minute),
		rawToken:                   "provider-submission-checkpoint-one-token",
		personalDetailsSubmittedAt: now.Add(-30 * time.Minute),
		identityIntentID:           uuid.MustParse("92e78155-ea80-4710-b330-9956bffb984d"),
		identityArtifactID:         uuid.MustParse("b955fa02-c802-4b51-96c7-a523f49bb19e"),
		identityIntentCreatedAt:    now.Add(-20 * time.Minute),
		identityConfirmedAt:        now.Add(-15 * time.Minute),
		biometricIntentID:          uuid.MustParse("3075545c-93ef-4834-820a-04943e7d5f62"),
		biometricArtifactID:        uuid.MustParse("1ced7420-fe92-40d4-82f1-ac6073e5f712"),
		biometricIntentCreatedAt:   now.Add(-10 * time.Minute),
		biometricConfirmedAt:       now.Add(-5 * time.Minute),
		applicantExpiresAt:         now.Add(30 * time.Minute),
		outboxID:                   uuid.MustParse("b9be4f53-c8d8-4935-ac85-f8b0914931bf"),
	}
}

// seedProviderSubmissionSuccessFixture is deliberately user-owned. Complete the
// ordered semantic history here; do not collapse it to a session-state-only seed.
func seedProviderSubmissionSuccessFixture(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	f providerSubmissionSuccessFixture,
) {
	t.Helper()
	tokens := session.NewProductionCryptoTokens()
	identityKey := fmt.Sprintf(
		"verification-sessions/%s/identity_document/%s",
		f.sessionID,
		f.identityIntentID,
	)
	biometricKey := fmt.Sprintf(
		"verification-sessions/%s/biometric_capture/%s",
		f.sessionID,
		f.biometricIntentID,
	)
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id,
			resume_token_hash,
			status,
			created_at,
			updated_at,
			expires_at
		)
		VALUES ($1, $2, 'biometric_capture_uploaded', $3, $4, $5)
	`,
		f.sessionID,
		tokens.Hash(f.rawToken),
		f.sessionCreatedAt,
		f.biometricConfirmedAt,
		f.applicantExpiresAt,
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
		VALUES ($1, 'Issue 009', DATE '2000-01-01', '3173000000000008', 'jalan boulevard', $2)
	`, f.sessionID, f.personalDetailsSubmittedAt); err != nil {
		t.Fatalf("insert immutable Personal Details prerequisite: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_personal_details', $2)
	`, f.sessionID, f.personalDetailsSubmittedAt); err != nil {
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
		f.identityIntentID,
		f.sessionID,
		identityKey,
		f.identityIntentCreatedAt,
		f.identityConfirmedAt,
		f.identityIntentCreatedAt.Add(30*time.Minute),
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
		f.identityArtifactID,
		f.identityIntentID,
		f.sessionID,
		identityKey,
		identityContentType,
		identitySizeBytes,
		identityETag,
		f.identityConfirmedAt,
	); err != nil {
		t.Fatalf("insert accepted Identity Document Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_identity_document', '{"outcome":"accepted"}', $2)
	`, f.sessionID, f.identityConfirmedAt); err != nil {
		t.Fatalf("insert accepted Identity Document Session Event: %v", err)
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
		VALUES ($1, $2, 'biometric_capture', $3, 'confirmed', $4, $5, $6, $5)
	`,
		f.biometricIntentID,
		f.sessionID,
		biometricKey,
		f.biometricIntentCreatedAt,
		f.biometricConfirmedAt,
		f.biometricIntentCreatedAt.Add(30*time.Minute),
	); err != nil {
		t.Fatalf("insert confirmed Biometric Capture Upload Intent: %v", err)
	}

	const biometricContentType = "image/jpeg"
	const biometricSizeBytes int64 = 2048
	const biometricETag = "biometric-capture-etag"
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
		VALUES ($1, $2, $3, 'biometric_capture', $4, $5, $6, $7, $8)
	`,
		f.biometricArtifactID,
		f.biometricIntentID,
		f.sessionID,
		biometricKey,
		biometricContentType,
		biometricSizeBytes,
		biometricETag,
		f.biometricConfirmedAt,
	); err != nil {
		t.Fatalf("insert accepted Biometric Capture Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_biometric_capture', '{"outcome":"accepted"}', $2)
	`, f.sessionID, f.biometricConfirmedAt); err != nil {
		t.Fatalf("insert accepted Biometric Capture Session Event: %v", err)
	}
}

func assertProviderSubmissionSuccessFixture(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	fixture providerSubmissionSuccessFixture,
) {
	t.Helper()

	tokens := session.NewProductionCryptoTokens()
	var status string
	var resumeTokenMatches bool
	var createdAt time.Time
	var updatedAt time.Time
	var applicantExpiresAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			resume_token_hash = $2,
			created_at,
			updated_at,
			expires_at
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID, tokens.Hash(fixture.rawToken)).Scan(
		&status,
		&resumeTokenMatches,
		&createdAt,
		&updatedAt,
		&applicantExpiresAt,
	); err != nil {
		t.Fatalf("read fixture Verification Session: %v", err)
	}
	if status != "biometric_capture_uploaded" ||
		!resumeTokenMatches ||
		!updatedAt.Equal(fixture.biometricConfirmedAt) ||
		!applicantExpiresAt.Equal(fixture.applicantExpiresAt) ||
		!createdAt.Equal(fixture.sessionCreatedAt) {
		t.Errorf(
			"fixture session = status:%q token-match:%t updated:%s expires:%s",
			status,
			resumeTokenMatches,
			updatedAt,
			applicantExpiresAt,
		)
	}

	var personalDetailsCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM personal_details
		WHERE verification_session_id = $1
		  AND created_at = $2
	`, fixture.sessionID, fixture.personalDetailsSubmittedAt).Scan(&personalDetailsCount); err != nil {
		t.Fatalf("count fixture Personal Details: %v", err)
	}
	if personalDetailsCount != 1 {
		t.Errorf("fixture Personal Details count = %d, want 1", personalDetailsCount)
	}

	var confirmedIntentCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM upload_intents
		WHERE verification_session_id = $1
		  AND status = 'confirmed'
		  AND (id, kind, confirmed_at) IN (
			($2, 'identity_document', $3::timestamptz),
			($4, 'biometric_capture', $5::timestamptz)
		  )
	`,
		fixture.sessionID,
		fixture.identityIntentID,
		fixture.identityConfirmedAt,
		fixture.biometricIntentID,
		fixture.biometricConfirmedAt,
	).Scan(&confirmedIntentCount); err != nil {
		t.Fatalf("count fixture confirmed Upload Intents: %v", err)
	}
	if confirmedIntentCount != 2 {
		t.Errorf("fixture confirmed Upload Intent count = %d, want 2", confirmedIntentCount)
	}

	var acceptedArtifactCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_artifacts
		WHERE verification_session_id = $1
		  AND (id, upload_intent_id, kind) IN (
			($2, $3, 'identity_document'),
			($4, $5, 'biometric_capture')
		  )
	`,
		fixture.sessionID,
		fixture.identityArtifactID,
		fixture.identityIntentID,
		fixture.biometricArtifactID,
		fixture.biometricIntentID,
	).Scan(&acceptedArtifactCount); err != nil {
		t.Fatalf("count fixture accepted Verification Artifacts: %v", err)
	}
	if acceptedArtifactCount != 2 {
		t.Errorf("fixture accepted Verification Artifact count = %d, want 2", acceptedArtifactCount)
	}

	rows, err := database.Query(ctx, `
		SELECT event_type
		FROM session_events
		WHERE session_id = $1
		ORDER BY occurred_at, id
	`, fixture.sessionID)
	if err != nil {
		t.Fatalf("read fixture Session Events: %v", err)
	}
	defer rows.Close()

	var eventTypes []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan fixture Session Event: %v", err)
		}
		eventTypes = append(eventTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate fixture Session Events: %v", err)
	}
	wantEventTypes := []string{
		"submit_personal_details",
		"confirm_identity_document",
		"confirm_biometric_capture",
	}
	if !slices.Equal(eventTypes, wantEventTypes) {
		t.Errorf("fixture Session Events = %v, want %v", eventTypes, wantEventTypes)
	}

	ready, err := postgresadapter.NewArtifactTransactions(database).
		HasRequiredAcceptedArtifacts(ctx, fixture.sessionID)
	if err != nil {
		t.Fatalf("transaction-compatible readiness check: %v", err)
	}
	if !ready {
		t.Fatal("fixture readiness = false, want true")
	}
}

func openProviderSubmissionDatabase(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	ctx, container, databaseURL := openUploadIntentDatabase(t)

	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect Provider Submission PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })
	return ctx, database
}

func (f providerSubmissionSuccessFixture) identityStorageKey() string {
	return fmt.Sprintf(
		"verification-sessions/%s/identity_document/%s",
		f.sessionID,
		f.identityIntentID,
	)
}

func (f providerSubmissionSuccessFixture) biometricStorageKey() string {
	return fmt.Sprintf(
		"verification-sessions/%s/biometric_capture/%s",
		f.sessionID,
		f.biometricIntentID,
	)
}
