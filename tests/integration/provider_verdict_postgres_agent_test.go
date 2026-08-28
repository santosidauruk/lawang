package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
)

func TestSignedStructurallyInvalidProviderVerdictsNeverPersist(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)
	handler := newProviderVerdictHandler(database, time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC))
	eventID := uuid.New()
	sessionID := uuid.New()

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "malformed JSON", body: []byte(`{"eventId":`)},
		{name: "unknown field", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"verified","extra":true}`, eventID, sessionID))},
		{name: "multiple JSON values", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"verified"}{}`, eventID, sessionID))},
		{name: "invalid event UUID", body: []byte(fmt.Sprintf(`{"eventId":"not-a-uuid","sessionId":%q,"verdict":"verified"}`, sessionID))},
		{name: "invalid session UUID", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":"not-a-uuid","verdict":"verified"}`, eventID))},
		{name: "invalid verdict", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"pending"}`, eventID, sessionID))},
		{name: "verified carries reason", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"verified","reason":"document_invalid"}`, eventID, sessionID))},
		{name: "rejected omits reason", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"rejected"}`, eventID, sessionID))},
		{name: "rejected null reason", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":null}`, eventID, sessionID))},
		{name: "rejected free-form reason", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":"provider_says_no"}`, eventID, sessionID))},
		{name: "rejected non-string reason", body: []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":42}`, eventID, sessionID))},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := sendProviderVerdictRequest(t, handler, test.body, "sha256="+signCheckpoint5WebhookBody(t, test.body))
			assertSafeProviderWebhookError(t, response, http.StatusInternalServerError, "INTERNAL", "internal server error")
			assertWebhookEventCount(t, ctx, database, 0)
		})
	}
}

func TestInvalidSignatureNeverPersistsProviderVerdict(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)
	handler := newProviderVerdictHandler(database, time.Date(2026, 8, 28, 13, 5, 0, 0, time.UTC))
	rawBody := []byte(fmt.Sprintf(
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		uuid.New(),
		uuid.New(),
	))

	response := sendProviderVerdictRequest(t, handler, rawBody, "sha256="+string(bytes.Repeat([]byte{'0'}, 64)))

	assertSafeProviderWebhookError(t, response, http.StatusUnauthorized, "INVALID_SIGNATURE", "invalid webhook signature")
	assertWebhookEventCount(t, ctx, database, 0)
}

func TestFirstSignedUnknownSessionVerdictIsDurablyIgnored(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)
	now := time.Date(2026, 8, 28, 13, 10, 0, 0, time.UTC)
	handler := newProviderVerdictHandler(database, now)
	eventID := uuid.New()
	reportedSessionID := uuid.New()
	rawBody := []byte(fmt.Sprintf(
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		eventID,
		reportedSessionID,
	))

	response := sendProviderVerdictRequest(t, handler, rawBody, "sha256="+signCheckpoint5WebhookBody(t, rawBody))

	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unknown-session response = status:%d body:%q, want exact 200 ok", response.Code, response.Body.String())
	}

	var storedSessionID uuid.UUID
	var storedPayload []byte
	var processingStatus, ignoreReason string
	var receivedAt, processedAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT reported_session_id, payload, processing_status, ignore_reason, received_at, processed_at
		FROM webhook_events
		WHERE id = $1
	`, eventID).Scan(&storedSessionID, &storedPayload, &processingStatus, &ignoreReason, &receivedAt, &processedAt); err != nil {
		t.Fatalf("read ignored unknown-session Webhook Event: %v", err)
	}
	if storedSessionID != reportedSessionID || !bytes.Equal(storedPayload, rawBody) ||
		processingStatus != "ignored" || ignoreReason != "unknown_session" ||
		!receivedAt.Equal(now) || !processedAt.Equal(now) {
		t.Errorf("ignored Webhook Event = session:%s raw-match:%t status:%q reason:%q received:%s processed:%s", storedSessionID, bytes.Equal(storedPayload, rawBody), processingStatus, ignoreReason, receivedAt, processedAt)
	}

	var sessionCount, sessionEventCount int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM verification_sessions WHERE id = $1`, reportedSessionID).Scan(&sessionCount); err != nil {
		t.Fatalf("count unknown Verification Session: %v", err)
	}
	if err := database.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id = $1`, reportedSessionID).Scan(&sessionEventCount); err != nil {
		t.Fatalf("count unknown-session Session Events: %v", err)
	}
	if sessionCount != 0 || sessionEventCount != 0 {
		t.Fatalf("unknown-session mutations = sessions:%d events:%d, want 0/0", sessionCount, sessionEventCount)
	}
}

func TestProviderVerdictFailuresRollbackEveryWrite(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)

	t.Run("Session Event failure rolls back verified outcome", func(t *testing.T) {
		fixture := newRejectedVerdictFixture(20)
		seedVerifiedVerdictFixture(t, ctx, database, fixture)
		installRejectingTrigger(t, ctx, database, `
			CREATE FUNCTION reject_checkpoint5_verification_event()
			RETURNS trigger
			LANGUAGE plpgsql
			AS $trigger$
			BEGIN
				IF NEW.event_type = 'verification_passed' THEN
					RAISE EXCEPTION 'forced verification event failure';
				END IF;
				RETURN NEW;
			END
			$trigger$;
			CREATE TRIGGER reject_checkpoint5_verification_event
			BEFORE INSERT ON session_events
			FOR EACH ROW
			EXECUTE FUNCTION reject_checkpoint5_verification_event();
		`, `
			DROP TRIGGER reject_checkpoint5_verification_event ON session_events;
			DROP FUNCTION reject_checkpoint5_verification_event();
		`)

		rawBody := []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`, fixture.providerEventID, fixture.sessionID))
		response := sendProviderVerdictRequest(t, newProviderVerdictHandler(database, fixture.processedAt), rawBody, "sha256="+signCheckpoint5WebhookBody(t, rawBody))
		assertSafeProviderWebhookError(t, response, http.StatusInternalServerError, "INTERNAL", "internal server error")
		assertPendingVerdictRollback(t, ctx, database, fixture)
	})

	t.Run("Webhook Event status failure rolls back rejected outcome", func(t *testing.T) {
		fixture := newRejectedVerdictFixture(21)
		seedVerifiedVerdictFixture(t, ctx, database, fixture)
		installRejectingTrigger(t, ctx, database, `
			CREATE FUNCTION reject_checkpoint5_webhook_status()
			RETURNS trigger
			LANGUAGE plpgsql
			AS $trigger$
			BEGIN
				IF NEW.processing_status = 'applied' THEN
					RAISE EXCEPTION 'forced Webhook Event status failure';
				END IF;
				RETURN NEW;
			END
			$trigger$;
			CREATE TRIGGER reject_checkpoint5_webhook_status
			BEFORE UPDATE OF processing_status ON webhook_events
			FOR EACH ROW
			EXECUTE FUNCTION reject_checkpoint5_webhook_status();
		`, `
			DROP TRIGGER reject_checkpoint5_webhook_status ON webhook_events;
			DROP FUNCTION reject_checkpoint5_webhook_status();
		`)

		rawBody := []byte(fmt.Sprintf(`{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":"document_invalid"}`, fixture.providerEventID, fixture.sessionID))
		response := sendProviderVerdictRequest(t, newProviderVerdictHandler(database, fixture.processedAt), rawBody, "sha256="+signCheckpoint5WebhookBody(t, rawBody))
		assertSafeProviderWebhookError(t, response, http.StatusInternalServerError, "INTERNAL", "internal server error")
		assertPendingVerdictRollback(t, ctx, database, fixture)
	})
}

func sendProviderVerdictRequest(t *testing.T, handler http.Handler, body []byte, signature string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(body))
	request.Header.Set("x-signature", signature)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertSafeProviderWebhookError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode, wantMessage string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var body httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode webhook error: %v; body=%s", err, response.Body.String())
	}
	if body.Code != wantCode || body.Message != wantMessage || body.Details != nil {
		t.Fatalf("webhook error = code:%q message:%q details:%v, want %q/%q/nil", body.Code, body.Message, body.Details, wantCode, wantMessage)
	}
}

func assertWebhookEventCount(t *testing.T, ctx context.Context, database *pgx.Conn, want int) {
	t.Helper()
	var count int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM webhook_events`).Scan(&count); err != nil {
		t.Fatalf("count Webhook Events: %v", err)
	}
	if count != want {
		t.Fatalf("Webhook Event count = %d, want %d", count, want)
	}
}

func installRejectingTrigger(t *testing.T, ctx context.Context, database *pgx.Conn, installSQL, cleanupSQL string) {
	t.Helper()
	if _, err := database.Exec(ctx, installSQL); err != nil {
		t.Fatalf("install forced-failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := database.Exec(context.Background(), cleanupSQL); err != nil {
			t.Errorf("remove forced-failure trigger: %v", err)
		}
	})
}

func assertPendingVerdictRollback(t *testing.T, ctx context.Context, database *pgx.Conn, fixture verifiedVerdictFixture) {
	t.Helper()
	var status string
	var verifiedAtIsNull, rejectedAtIsNull, rejectionReasonIsNull bool
	if err := database.QueryRow(ctx, `
		SELECT status, verified_at IS NULL, rejected_at IS NULL, rejection_reason IS NULL
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&status, &verifiedAtIsNull, &rejectedAtIsNull, &rejectionReasonIsNull); err != nil {
		t.Fatalf("read rolled-back Verification Session: %v", err)
	}
	if status != "verification_pending" || !verifiedAtIsNull || !rejectedAtIsNull || !rejectionReasonIsNull {
		t.Fatalf("rolled-back session = status:%q verified-null:%t rejected-null:%t reason-null:%t", status, verifiedAtIsNull, rejectedAtIsNull, rejectionReasonIsNull)
	}

	var webhookCount, verificationEventCount int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE id = $1`, fixture.providerEventID).Scan(&webhookCount); err != nil {
		t.Fatalf("count rolled-back Webhook Events: %v", err)
	}
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM session_events
		WHERE session_id = $1
		  AND event_type IN ('verification_passed', 'verification_failed')
	`, fixture.sessionID).Scan(&verificationEventCount); err != nil {
		t.Fatalf("count rolled-back verdict Session Events: %v", err)
	}
	if webhookCount != 0 || verificationEventCount != 0 {
		t.Fatalf("rolled-back verdict writes = webhooks:%d verification-events:%d, want 0/0", webhookCount, verificationEventCount)
	}
}
