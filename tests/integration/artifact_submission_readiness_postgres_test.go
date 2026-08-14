package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

// TestAcceptedIdentityAndBiometricArtifactsMakeSessionSubmissionReady is the
// user-owned success tracer for Issue 008 Checkpoint 4.
//
// Keep this as one behavior: two accepted Verification Artifacts with the exact
// required kinds, both owned by one Verification Session, make that session ready
// for a future Provider Submission. This test must not create a public readiness
// endpoint, submit the session, or treat the result as a durable reservation.
func TestAcceptedIdentityAndBiometricArtifactsMakeSessionSubmissionReady(t *testing.T) {
	// ARRANGE 1 — fixed facts and disposable PostgreSQL
	ctx, database := openArtifactDatabase(t)
	now := time.Date(2026, 8, 14, 14, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("a2a43c27-d2fc-4436-aa41-1c00d62b4c43")
	identityIntentID := uuid.MustParse("45c35f0a-89a9-4607-87e6-30cfbdc7fdf0")
	identityArtifactID := uuid.MustParse("fe7b0fb7-4950-4ee4-8961-c908212dd0bb")
	biometricIntentID := uuid.MustParse("822b3fe2-d0cd-4633-afc7-f4f821f8638a")
	biometricArtifactID := uuid.MustParse("3f447f92-c2b4-4cc1-aed2-b4e42e4cc8ad")
	identityCreatedAt := now.Add(-10 * time.Minute)
	identityConfirmedAt := now.Add(-5 * time.Minute)
	identityExpiresAt := now.Add(5 * time.Minute)
	biometricIntentCreatedAt := now.Add(-time.Minute)
	biometricConfirmedAt := now.Add(1 * time.Minute)
	biometricIntentExpiresAt := now.Add(10 * time.Minute)

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

	// ARRANGE 2 — one same-session accepted-artifact fixture
	// TODO(user): seed one Verification Session. Its state may be
	// biometric_capture_uploaded so the row represents a valid completed history,
	// but the readiness query itself must not derive its answer from session state.
	//
	tokens := session.NewProductionCryptoTokens()
	rawToken := "readiness-token-" + uuid.NewString()
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
	`, sessionID, tokens.Hash(rawToken), now.Add(-10*time.Minute), now, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("insert fixture Verification Session: %v", err)
	}

	// TODO(user): seed one confirmed identity_document Upload Intent and its accepted
	// Verification Artifact. Keep intent ID, session ID, kind, and storage key
	// ownership exact so PostgreSQL constraints remain authoritative.
	//
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
		identityCreatedAt,
		identityConfirmedAt,
		identityExpiresAt,
	); err != nil {
		t.Fatalf("insert fixture confirmed Identity Document Upload Intent: %v", err)
	}

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
		VALUES ($1, $2, $3, 'identity_document', $4, 'image/jpeg', 2048, $5, $6)
	`,
		identityArtifactID,
		identityIntentID,
		sessionID,
		identityKey,
		"identity-etag-"+identityIntentID.String(),
		identityConfirmedAt,
	); err != nil {
		t.Fatalf("insert fixture accepted Identity Document Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_personal_details', $2)
	`, sessionID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("insert fixture Personal Details Session Event: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_identity_document', '{"outcome":"accepted"}', $2)
	`, sessionID, identityConfirmedAt); err != nil {
		t.Fatalf("insert fixture Identity Document Session Event: %v", err)
	}
	// TODO(user): seed one confirmed biometric_capture Upload Intent and its accepted
	// Verification Artifact for the same session, with a distinct intent ID and key.
	// No object-storage fake or stored object belongs in this test.

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
		VALUES ($1, $2, 'biometric_capture', $3, 'confirmed' , $4, $5, $6, $5)
	`,
		biometricIntentID,
		sessionID,
		biometricKey,
		biometricIntentCreatedAt,
		biometricConfirmedAt,
		biometricIntentExpiresAt,
	); err != nil {
		t.Fatalf("insert fixture pending Biometric Capture Upload Intent: %v", err)
	}

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
		VALUES ($1, $2, $3, 'biometric_capture', $4, 'image/jpeg', 2048, $5, $6)
	`,
		biometricArtifactID,
		biometricIntentID,
		sessionID,
		biometricKey,
		"biometric-etag-"+biometricIntentID.String(),
		biometricConfirmedAt,
	); err != nil {
		t.Fatalf("insert fixture accepted Biometric Capture Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'confirm_biometric_capture', $2)
	`, sessionID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("insert fixture Biometric Capture Session Event: %v", err)
	}

	// ACT — internal, transaction-compatible readiness boundary
	// TODO(user): call the narrow PostgreSQL-backed boundary with sessionID. Let the
	// first meaningful compile/test failure expose the missing production query or
	// adapter method. Do not query database rows directly from the assertion: the
	// tracer must exercise the production boundary that Issue 009 can call from its
	// guarded transaction.
	artifacts := postgres.NewArtifactTransactions(database)

	ready, err := artifacts.HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("Has Required Accepted Artifacts is error: %v", err)
	}

	// ASSERT — one observable answer
	// TODO(user): require no error and readiness == true. The reason must be the two
	// exact accepted artifact kinds owned by sessionID, not Upload Intent status,
	// object presence, or session state alone.
	if !ready {
		t.Errorf("Has Required Accepted Artifacts is %t, want true", ready)
	}

	// These anchors keep the scaffold compiling until the user-owned fixture and call
	// replace them. Delete this block together with Skip when beginning the tracer.

}
