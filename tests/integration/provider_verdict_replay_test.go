package integration_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExactDuplicateProviderCallbackReturnsRecordedSuccessWithoutMutation(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)
	fixture := newVerifiedVerdictFixture()
	seedVerifiedVerdictFixture(t, ctx, database, fixture)
	body := fmt.Appendf(
		nil,
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		fixture.providerEventID,
		fixture.sessionID,
	)

	firstHandler := newProviderVerdictHandler(database, fixture.processedAt)
	first := sendProviderVerdictRequest(t, firstHandler, body, "sha256="+signCheckpoint5WebhookBody(t, body))
	if first.Code != http.StatusOK || first.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("first callback = %d/%q, want exact 200 ok", first.Code, first.Body.String())
	}

	replayAt := fixture.processedAt.Add(time.Minute)
	replayHandler := newProviderVerdictHandler(database, replayAt)
	replay := sendProviderVerdictRequest(t, replayHandler, body, "sha256="+signCheckpoint5WebhookBody(t, body))
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf(
			"duplicate responses = first:%d/%q replay:%d/%q",
			first.Code,
			first.Body.String(),
			replay.Code,
			replay.Body.String(),
		)
	}

	var status string
	var updatedAt, verifiedAt, webhookProcessedAt time.Time
	var webhookCount, terminalEventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			vs.status,
			vs.updated_at,
			vs.verified_at,
			we.processed_at,
			(SELECT count(*) FROM webhook_events WHERE id = $2),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $1 AND event_type = 'verification_passed')
		FROM verification_sessions vs
		JOIN webhook_events we ON we.id = $2
		WHERE vs.id = $1
	`, fixture.sessionID, fixture.providerEventID).Scan(
		&status,
		&updatedAt,
		&verifiedAt,
		&webhookProcessedAt,
		&webhookCount,
		&terminalEventCount,
	); err != nil {
		t.Fatalf("read duplicate callback outcome: %v", err)
	}

	if status != "verified" ||
		!updatedAt.Equal(fixture.processedAt) ||
		!verifiedAt.Equal(fixture.processedAt) ||
		!webhookProcessedAt.Equal(fixture.processedAt) ||
		webhookCount != 1 ||
		terminalEventCount != 1 {
		t.Fatalf(
			"duplicate outcome = status:%s updated:%s verified:%s processed:%s webhooks:%d events:%d; replay clock:%s",
			status,
			updatedAt,
			verifiedAt,
			webhookProcessedAt,
			webhookCount,
			terminalEventCount,
			replayAt,
		)
	}
}

func TestLateProviderCallbackIsDurablyIgnoredWithoutTerminalMutation(t *testing.T) {
	ctx, database := openProviderVerdictDatabase(t)
	fixture := newVerifiedVerdictFixture()
	seedVerifiedVerdictFixture(t, ctx, database, fixture)
	firstBody := fmt.Appendf(
		nil,
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		fixture.providerEventID,
		fixture.sessionID,
	)
	firstHandler := newProviderVerdictHandler(database, fixture.processedAt)
	first := sendProviderVerdictRequest(t, firstHandler, firstBody, "sha256="+signCheckpoint5WebhookBody(t, firstBody))
	if first.Code != http.StatusOK || first.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("first callback = %d/%q, want exact 200 ok", first.Code, first.Body.String())
	}

	lateEventID := uuid.New()
	lateAt := fixture.processedAt.Add(time.Minute)
	lateBody := fmt.Appendf(
		nil,
		`{"eventId":%q,"sessionId":%q,"verdict":"rejected","reason":"suspected_fraud"}`,
		lateEventID,
		fixture.sessionID,
	)
	lateHandler := newProviderVerdictHandler(database, lateAt)
	late := sendProviderVerdictRequest(t, lateHandler, lateBody, "sha256="+signCheckpoint5WebhookBody(t, lateBody))
	if late.Code != http.StatusOK || late.Body.String() != first.Body.String() {
		t.Fatalf(
			"late callback = %d/%q, want recorded success %d/%q",
			late.Code,
			late.Body.String(),
			first.Code,
			first.Body.String(),
		)
	}

	var status, processingStatus, ignoreReason string
	var updatedAt, verifiedAt, receivedAt, processedAt time.Time
	var rejectedAtIsNull, rejectionReasonIsNull bool
	var payload []byte
	var webhookCount, terminalEventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			vs.status,
			vs.updated_at,
			vs.verified_at,
			vs.rejected_at IS NULL,
			vs.rejection_reason IS NULL,
			we.processing_status,
			we.ignore_reason,
			we.payload,
			we.received_at,
			we.processed_at,
			(SELECT count(*) FROM webhook_events WHERE reported_session_id = $1),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $1 AND event_type IN ('verification_passed', 'verification_failed'))
		FROM verification_sessions vs
		JOIN webhook_events we ON we.id = $2
		WHERE vs.id = $1
	`, fixture.sessionID, lateEventID).Scan(
		&status,
		&updatedAt,
		&verifiedAt,
		&rejectedAtIsNull,
		&rejectionReasonIsNull,
		&processingStatus,
		&ignoreReason,
		&payload,
		&receivedAt,
		&processedAt,
		&webhookCount,
		&terminalEventCount,
	); err != nil {
		t.Fatalf("read late callback outcome: %v", err)
	}

	if status != "verified" ||
		!updatedAt.Equal(fixture.processedAt) ||
		!verifiedAt.Equal(fixture.processedAt) ||
		!rejectedAtIsNull ||
		!rejectionReasonIsNull ||
		processingStatus != "ignored" ||
		ignoreReason != "terminal_session" ||
		!bytes.Equal(payload, lateBody) ||
		!receivedAt.Equal(lateAt) ||
		!processedAt.Equal(lateAt) ||
		webhookCount != 2 ||
		terminalEventCount != 1 {
		t.Fatalf(
			"late outcome = session:%s updated:%s verified:%s rejected-null:%t reason-null:%t webhook:%s/%s raw:%t received:%s processed:%s webhooks:%d terminal-events:%d",
			status,
			updatedAt,
			verifiedAt,
			rejectedAtIsNull,
			rejectionReasonIsNull,
			processingStatus,
			ignoreReason,
			bytes.Equal(payload, lateBody),
			receivedAt,
			processedAt,
			webhookCount,
			terminalEventCount,
		)
	}
}
