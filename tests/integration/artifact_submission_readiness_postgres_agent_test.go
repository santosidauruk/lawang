package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/session"
)

func TestSubmissionReadinessRejectsSingleAcceptedArtifact(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	readiness := postgresadapter.NewArtifactTransactions(database)
	now := time.Date(2026, 8, 14, 15, 0, 0, 0, time.UTC)

	for _, onlyKind := range []string{"identity_document", "biometric_capture"} {
		t.Run(onlyKind, func(t *testing.T) {
			sessionID := seedSubmissionReadinessSession(t, ctx, database, now)
			seedAcceptedReadinessArtifact(t, ctx, database, sessionID, onlyKind, now)

			ready, err := readiness.HasRequiredAcceptedArtifacts(ctx, sessionID)
			if err != nil {
				t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
			}
			if ready {
				t.Fatalf(
					"HasRequiredAcceptedArtifacts() = true with only %s, want false",
					onlyKind,
				)
			}
		})
	}
}

func TestSubmissionReadinessRejectsStoredObjectWithoutAcceptedArtifact(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 16, 0, 0, 0, time.UTC)
	sessionID := seedSubmissionReadinessSession(t, ctx, database, now)
	_, storageKey := seedPendingReadinessIntent(
		t,
		ctx,
		database,
		sessionID,
		"identity_document",
		now,
	)
	storedObjects := map[string]bool{storageKey: true}
	if !storedObjects[storageKey] {
		t.Fatalf("stored object fixture for %q is absent", storageKey)
	}

	ready, err := postgresadapter.NewArtifactTransactions(database).
		HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if ready {
		t.Fatal("HasRequiredAcceptedArtifacts() = true with stored object only, want false")
	}
}

func TestSubmissionReadinessRejectsConfirmedBiometricIntentWithoutAcceptedArtifact(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 17, 0, 0, 0, time.UTC)
	sessionID := seedSubmissionReadinessSession(t, ctx, database, now)
	seedConfirmedReadinessIntent(
		t,
		ctx,
		database,
		sessionID,
		"biometric_capture",
		now,
	)

	ready, err := postgresadapter.NewArtifactTransactions(database).
		HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if ready {
		t.Fatal("HasRequiredAcceptedArtifacts() = true with confirmed intent only, want false")
	}
}

func TestSubmissionReadinessRejectsValidationFailedIdentityIntentWithBiometricArtifact(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	sessionID := seedSubmissionReadinessSession(t, ctx, database, now)
	seedValidationFailedIdentityIntent(t, ctx, database, sessionID, now)
	seedAcceptedReadinessArtifact(
		t,
		ctx,
		database,
		sessionID,
		"biometric_capture",
		now,
	)

	ready, err := postgresadapter.NewArtifactTransactions(database).
		HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if ready {
		t.Fatal("HasRequiredAcceptedArtifacts() = true with failed identity intent, want false")
	}
}

func TestSubmissionReadinessRejectsRequiredArtifactsSplitAcrossSessions(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 19, 0, 0, 0, time.UTC)
	identitySessionID := seedSubmissionReadinessSession(t, ctx, database, now)
	biometricSessionID := seedSubmissionReadinessSession(
		t,
		ctx,
		database,
		now.Add(time.Second),
	)
	seedAcceptedReadinessArtifact(
		t,
		ctx,
		database,
		identitySessionID,
		"identity_document",
		now,
	)
	seedAcceptedReadinessArtifact(
		t,
		ctx,
		database,
		biometricSessionID,
		"biometric_capture",
		now,
	)

	readiness := postgresadapter.NewArtifactTransactions(database)
	for _, sessionID := range []uuid.UUID{identitySessionID, biometricSessionID} {
		ready, err := readiness.HasRequiredAcceptedArtifacts(ctx, sessionID)
		if err != nil {
			t.Fatalf("HasRequiredAcceptedArtifacts(%s) error = %v", sessionID, err)
		}
		if ready {
			t.Fatalf(
				"HasRequiredAcceptedArtifacts(%s) = true with artifacts split across sessions",
				sessionID,
			)
		}
	}
}

func TestSubmissionReadinessPredicateRunsInsideExistingTransaction(t *testing.T) {
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	sessionID := seedSubmissionReadinessSession(t, ctx, database, now)
	seedAcceptedReadinessArtifact(
		t,
		ctx,
		database,
		sessionID,
		"identity_document",
		now,
	)
	seedAcceptedReadinessArtifact(
		t,
		ctx,
		database,
		sessionID,
		"biometric_capture",
		now,
	)

	tx, err := database.Begin(ctx)
	if err != nil {
		t.Fatalf("begin guarded submission transaction fixture: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	ready, err := postgresadapter.NewArtifactTransactions(tx).
		HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("transaction-bound HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if !ready {
		t.Fatal("transaction-bound HasRequiredAcceptedArtifacts() = false, want true")
	}
}

func seedSubmissionReadinessSession(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	now time.Time,
) uuid.UUID {
	t.Helper()

	sessionID := uuid.New()
	rawToken := "submission-readiness-" + sessionID.String()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id,
			resume_token_hash,
			status,
			created_at,
			updated_at,
			expires_at
		)
		VALUES ($1, $2, 'biometric_capture_uploaded', $3, $3, $4)
	`,
		sessionID,
		session.NewProductionCryptoTokens().Hash(rawToken),
		now,
		now.Add(30*time.Minute),
	); err != nil {
		t.Fatalf("insert readiness Verification Session: %v", err)
	}
	return sessionID
}

func seedAcceptedReadinessArtifact(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	kind string,
	now time.Time,
) {
	t.Helper()

	intentID, storageKey := seedConfirmedReadinessIntent(
		t,
		ctx,
		database,
		sessionID,
		kind,
		now,
	)

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
		VALUES ($1, $2, $3, $4, $5, 'image/jpeg', 2048, $6, $7)
	`,
		uuid.New(),
		intentID,
		sessionID,
		kind,
		storageKey,
		kind+"-accepted-etag-"+intentID.String(),
		now,
	); err != nil {
		t.Fatalf("insert accepted %s Verification Artifact: %v", kind, err)
	}
}

func seedConfirmedReadinessIntent(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	kind string,
	now time.Time,
) (uuid.UUID, string) {
	t.Helper()

	intentID := uuid.New()
	storageKey := readinessStorageKey(sessionID, kind, intentID)
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
		VALUES ($1, $2, $3, $4, 'confirmed', $5, $5, $6, $5)
	`, intentID, sessionID, kind, storageKey, now, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert confirmed %s Upload Intent: %v", kind, err)
	}
	return intentID, storageKey
}

func seedPendingReadinessIntent(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	kind string,
	now time.Time,
) (uuid.UUID, string) {
	t.Helper()

	intentID := uuid.New()
	storageKey := readinessStorageKey(sessionID, kind, intentID)
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
		VALUES ($1, $2, $3, $4, $5, $5, $6)
	`, intentID, sessionID, kind, storageKey, now, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert pending %s Upload Intent: %v", kind, err)
	}
	return intentID, storageKey
}

func seedValidationFailedIdentityIntent(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	now time.Time,
) {
	t.Helper()

	intentID := uuid.New()
	storageKey := readinessStorageKey(
		sessionID,
		"identity_document",
		intentID,
	)
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
			failure_code
		)
		VALUES (
			$1,
			$2,
			'identity_document',
			$3,
			'validation_failed',
			$4,
			$4,
			$5,
			'identity_number_mismatch'
		)
	`, intentID, sessionID, storageKey, now, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert validation-failed Identity Upload Intent: %v", err)
	}
}

func readinessStorageKey(sessionID uuid.UUID, kind string, intentID uuid.UUID) string {
	return fmt.Sprintf(
		"verification-sessions/%s/%s/%s",
		sessionID,
		kind,
		intentID,
	)
}
