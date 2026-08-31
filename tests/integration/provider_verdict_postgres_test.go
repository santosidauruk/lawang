package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/providerverdict"
)

const checkpoint5WebhookSecret = "checkpoint-5-fixed-webhook-secret"

// TestFirstSignedVerifiedVerdictCommitsOnePostgreSQLOutcome is the user-owned
// success tracer for Issue 009 Checkpoint 5.
//
// Keep this as one behavior: a first, structurally valid, correctly signed verified
// callback commits the Webhook Event, terminal Verification Session fields, and
// verification_passed Session Event as one PostgreSQL unit. Rejected reasons,
// malformed/rollback siblings, unknown-session handling, and duplicate/late
// callbacks are deliberately outside this tracer.
func TestFirstSignedVerifiedVerdictCommitsOnePostgreSQLOutcome(t *testing.T) {
	// ARRANGE — the agent-owned fixture establishes the complete semantic history
	// through verification_pending in disposable PostgreSQL.
	ctx, database := openProviderVerdictDatabase(t)
	fixture := newVerifiedVerdictFixture()
	seedVerifiedVerdictFixture(t, ctx, database, fixture)

	rawBody := fmt.Appendf(nil,
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		fixture.providerEventID,
		fixture.sessionID,
	)
	signature := signCheckpoint5WebhookBody(t, rawBody)

	// ACT — keep the public signed HTTP boundary here. Extend the production
	// Provider Verdict service with the user-authored PostgreSQL transaction and
	// update this constructor call when that explicit dependency is introduced.
	verdictTransactor := postgres.NewProviderVerdictTransactions(database)
	service := providerverdict.NewProviderVerdictService(fixedClock{now: fixture.processedAt}, verdictTransactor)
	handler := httpapi.NewHandler(nil, nil, nil, nil, &httpapi.VerifiedBody{
		Service:               service,
		ProviderWebhookSecret: checkpoint5WebhookSecret,
	}, nil)
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		bytes.NewReader(rawBody),
	)
	request.Header.Set("x-signature", "sha256="+signature)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf(
			"signed webhook response = status:%d body:%q, want status:200 exact ok body",
			response.Code,
			response.Body.String(),
		)
	}

	// ASSERT — read durable state directly. Raw SQL is intentional here: assertions
	// must not pass because an application mapper returns the expected values.
	var status string
	var updatedAt time.Time
	var verifiedAt time.Time
	var rejectedAtIsNull bool
	var rejectionReasonIsNull bool
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			updated_at,
			verified_at,
			rejected_at IS NULL,
			rejection_reason IS NULL
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(
		&status,
		&updatedAt,
		&verifiedAt,
		&rejectedAtIsNull,
		&rejectionReasonIsNull,
	); err != nil {
		t.Fatalf("read verified Verification Session: %v", err)
	}
	if status != "verified" ||
		!updatedAt.Equal(fixture.processedAt) ||
		!verifiedAt.Equal(fixture.processedAt) ||
		!rejectedAtIsNull ||
		!rejectionReasonIsNull {
		t.Errorf(
			"verified session = status:%q updated:%s verified:%s rejected-null:%t reason-null:%t",
			status,
			updatedAt,
			verifiedAt,
			rejectedAtIsNull,
			rejectionReasonIsNull,
		)
	}

	var reportedSessionID uuid.UUID
	var storedRawBody []byte
	var processingStatus string
	var ignoreReasonIsNull bool
	var receivedAt time.Time
	var processedAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT
			reported_session_id,
			payload,
			processing_status,
			ignore_reason IS NULL,
			received_at,
			processed_at
		FROM webhook_events
		WHERE id = $1
	`, fixture.providerEventID).Scan(
		&reportedSessionID,
		&storedRawBody,
		&processingStatus,
		&ignoreReasonIsNull,
		&receivedAt,
		&processedAt,
	); err != nil {
		t.Fatalf("read applied Webhook Event: %v", err)
	}
	if reportedSessionID != fixture.sessionID ||
		!bytes.Equal(storedRawBody, rawBody) ||
		processingStatus != "applied" ||
		!ignoreReasonIsNull ||
		!receivedAt.Equal(fixture.processedAt) ||
		!processedAt.Equal(fixture.processedAt) {
		t.Errorf(
			"Webhook Event = session:%s raw-match:%t status:%q ignore-null:%t received:%s processed:%s",
			reportedSessionID,
			bytes.Equal(storedRawBody, rawBody),
			processingStatus,
			ignoreReasonIsNull,
			receivedAt,
			processedAt,
		)
	}

	var passedEventCount int
	var passedEventIsExact bool
	if err := database.QueryRow(ctx, `
		SELECT
			count(*),
			COALESCE(bool_and(metadata = '{}'::jsonb AND occurred_at = $2), false)
		FROM session_events
		WHERE session_id = $1
		  AND event_type = 'verification_passed'
	`, fixture.sessionID, fixture.processedAt).Scan(
		&passedEventCount,
		&passedEventIsExact,
	); err != nil {
		t.Fatalf("read verification_passed Session Event: %v", err)
	}
	if passedEventCount != 1 || !passedEventIsExact {
		t.Errorf(
			"verification_passed events = count:%d exact:%t, want count:1 exact:true",
			passedEventCount,
			passedEventIsExact,
		)
	}
}

func TestFirstSignedRejectedVerdictCommitsBoundedPostgreSQLOutcome(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)

	for index, reason := range []string{
		"document_invalid",
		"biometric_mismatch",
		"identity_not_verified",
		"suspected_fraud",
	} {
		t.Run(reason, func(t *testing.T) {
			fixture := newRejectedVerdictFixture(index)
			seedVerifiedVerdictFixture(t, ctx, database, fixture)
			rawBody := fmt.Appendf(nil, `{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":%q}`,
				fixture.providerEventID,
				fixture.sessionID,
				reason,
			)

			handler := newProviderVerdictHandler(database, fixture.processedAt)
			request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(rawBody))
			request.Header.Set("x-signature", "sha256="+signCheckpoint5WebhookBody(t, rawBody))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
				t.Fatalf("signed rejected webhook response = status:%d body:%q, want status:200 exact ok body", response.Code, response.Body.String())
			}

			var status string
			var updatedAt, rejectedAt time.Time
			var verifiedAtIsNull bool
			var storedReason string
			if err := database.QueryRow(ctx, `
				SELECT status, updated_at, rejected_at, verified_at IS NULL, rejection_reason
				FROM verification_sessions
				WHERE id = $1
			`, fixture.sessionID).Scan(&status, &updatedAt, &rejectedAt, &verifiedAtIsNull, &storedReason); err != nil {
				t.Fatalf("read rejected Verification Session: %v", err)
			}
			if status != "rejected" || !updatedAt.Equal(fixture.processedAt) ||
				!rejectedAt.Equal(fixture.processedAt) || !verifiedAtIsNull || storedReason != reason {
				t.Errorf("rejected session = status:%q updated:%s rejected:%s verified-null:%t reason:%q", status, updatedAt, rejectedAt, verifiedAtIsNull, storedReason)
			}

			var storedSessionID uuid.UUID
			var storedPayload []byte
			var processingStatus string
			var ignoreReasonIsNull bool
			var receivedAt, processedAt time.Time
			if err := database.QueryRow(ctx, `
				SELECT reported_session_id, payload, processing_status, ignore_reason IS NULL, received_at, processed_at
				FROM webhook_events
				WHERE id = $1
			`, fixture.providerEventID).Scan(
				&storedSessionID, &storedPayload, &processingStatus, &ignoreReasonIsNull, &receivedAt, &processedAt,
			); err != nil {
				t.Fatalf("read rejected Webhook Event: %v", err)
			}
			if storedSessionID != fixture.sessionID || !bytes.Equal(storedPayload, rawBody) ||
				processingStatus != "applied" || !ignoreReasonIsNull ||
				!receivedAt.Equal(fixture.processedAt) || !processedAt.Equal(fixture.processedAt) {
				t.Errorf("rejected Webhook Event = session:%s raw-match:%t status:%q ignore-null:%t received:%s processed:%s", storedSessionID, bytes.Equal(storedPayload, rawBody), processingStatus, ignoreReasonIsNull, receivedAt, processedAt)
			}

			var failedEventCount int
			var failedEventIsExact bool
			if err := database.QueryRow(ctx, `
				SELECT count(*), COALESCE(bool_and(metadata = '{}'::jsonb AND occurred_at = $2), false)
				FROM session_events
				WHERE session_id = $1 AND event_type = 'verification_failed'
			`, fixture.sessionID, fixture.processedAt).Scan(&failedEventCount, &failedEventIsExact); err != nil {
				t.Fatalf("read verification_failed Session Event: %v", err)
			}
			if failedEventCount != 1 || !failedEventIsExact {
				t.Errorf("verification_failed events = count:%d exact:%t, want count:1 exact:true", failedEventCount, failedEventIsExact)
			}
		})
	}

	var freeFormReasonCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM verification_sessions
		WHERE status = 'rejected'
		  AND rejection_reason NOT IN (
			'document_invalid',
			'biometric_mismatch',
			'identity_not_verified',
			'suspected_fraud'
		  )
	`).Scan(&freeFormReasonCount); err != nil {
		t.Fatalf("count free-form rejection reasons: %v", err)
	}
	if freeFormReasonCount != 0 {
		t.Fatalf("free-form rejection reason rows = %d, want 0", freeFormReasonCount)
	}
}

type verifiedVerdictFixture struct {
	prerequisite    providerSubmissionSuccessFixture
	sessionID       uuid.UUID
	providerEventID uuid.UUID
	submittedAt     time.Time
	processedAt     time.Time
}

func newVerifiedVerdictFixture() verifiedVerdictFixture {
	prerequisite := newProviderSubmissionSuccessFixture()
	return verifiedVerdictFixture{
		prerequisite:    prerequisite,
		sessionID:       prerequisite.sessionID,
		providerEventID: uuid.MustParse("897c8981-79ca-4ef3-91cc-23a809c147ce"),
		submittedAt:     prerequisite.now,
		processedAt:     prerequisite.now.Add(5 * time.Minute),
	}
}

func newRejectedVerdictFixture(index int) verifiedVerdictFixture {
	fixture := newVerifiedVerdictFixture()
	fixture.prerequisite.rawToken = fmt.Sprintf("checkpoint-five-rejected-token-%d", index)
	fixture.prerequisite.sessionID = uuid.New()
	fixture.prerequisite.identityIntentID = uuid.New()
	fixture.prerequisite.identityArtifactID = uuid.New()
	fixture.prerequisite.biometricIntentID = uuid.New()
	fixture.prerequisite.biometricArtifactID = uuid.New()
	fixture.prerequisite.outboxID = uuid.New()
	fixture.sessionID = fixture.prerequisite.sessionID
	fixture.providerEventID = uuid.New()
	fixture.processedAt = fixture.processedAt.Add(time.Duration(index) * time.Minute)
	return fixture
}

func newProviderVerdictHandler(database *pgx.Conn, now time.Time) http.Handler {
	verdictTransactor := postgres.NewProviderVerdictTransactions(database)
	service := providerverdict.NewProviderVerdictService(fixedClock{now: now}, verdictTransactor)
	return httpapi.NewHandler(nil, nil, nil, nil, &httpapi.VerifiedBody{
		Service:               service,
		ProviderWebhookSecret: checkpoint5WebhookSecret,
	}, nil)
}

func seedVerifiedVerdictFixture(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	fixture verifiedVerdictFixture,
) {
	t.Helper()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture.prerequisite)

	if _, err := database.Exec(ctx, `
		UPDATE verification_sessions
		SET
			status = 'verification_pending',
			updated_at = $2::timestamptz,
			verification_deadline_at = $2::timestamptz + interval '24 hours'
		WHERE id = $1
	`, fixture.sessionID, fixture.submittedAt); err != nil {
		t.Fatalf("advance fixture Verification Session to pending: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'submit_session', '{}'::jsonb, $2)
	`, fixture.sessionID, fixture.submittedAt); err != nil {
		t.Fatalf("insert submit_session fixture event: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO outbox (
			id,
			verification_session_id,
			task_type,
			payload,
			created_at
		)
		VALUES ($1, $2, 'provider:submit', jsonb_build_object('sessionId', $2::uuid), $3)
	`, fixture.prerequisite.outboxID, fixture.sessionID, fixture.submittedAt); err != nil {
		t.Fatalf("insert durable provider submission outbox fixture: %v", err)
	}
}

func signCheckpoint5WebhookBody(t *testing.T, rawBody []byte) string {
	t.Helper()
	mac, err := httpapi.SetHmacSubmissionBody(checkpoint5WebhookSecret, rawBody)
	if err != nil {
		t.Fatalf("sign exact Checkpoint 5 webhook body: %v", err)
	}
	return hex.EncodeToString(mac)
}

func openProviderVerdictDatabase(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
	ctx, container, databaseURL := openUploadIntentDatabase(t)

	// This runner deliberately applies the user-owned migration even while it is an
	// empty scaffold, so the focused test turns GREEN only after real schema exists.
	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
		{"../../sql/migrations/00008_create_webhook_events.sql", "/tmp/00008.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect Provider Verdict PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })
	return ctx, database
}
