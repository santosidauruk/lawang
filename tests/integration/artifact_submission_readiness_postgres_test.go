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

	// ARRANGE 2 — one same-session accepted-artifact fixture. The state represents a
	// completed history, but the readiness query must not derive its answer from it.
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

	// Seed one confirmed Identity Document Upload Intent and its accepted Verification
	// Artifact with exact intent/session/kind/key ownership.
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
	// Seed one confirmed Biometric Capture Upload Intent and its accepted Verification
	// Artifact for the same session, with a distinct intent ID and key.

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
		t.Fatalf("insert fixture confirmed Biometric Capture Upload Intent: %v", err)
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

	// ACT — internal, transaction-compatible artifact predicate
	artifacts := postgres.NewArtifactTransactions(database)

	ready, err := artifacts.HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}

	// ASSERT — one observable answer
	if !ready {
		t.Errorf("HasRequiredAcceptedArtifacts() = %t, want true", ready)
	}
}
