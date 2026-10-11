package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TestAuditCLIPrintsOnlyBoundedSessionHistory is the Checkpoint 5 RED tracer.
// The fixture deliberately stores sensitive values in adjacent tables. The user
// owns the production queries, reader, read model, and command that make it GREEN.
func TestAuditCLIPrintsOnlyBoundedSessionHistory(t *testing.T) {
	ctx, container, databaseURL := openUploadIntentDatabase(t)
	runPSQLFile(t, ctx, container,
		"../../sql/migrations/00008_create_webhook_events.sql", "/tmp/00008.sql")
	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect audit fixture database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })

	fixture := seedSensitiveAuditFixture(t, ctx, database)
	command := exec.CommandContext(ctx, "go", "run", "./cmd/audit", "--session-id", fixture.sessionID.String())
	command.Dir = "../.."
	command.Env = append(os.Environ(), "DATABASE_URL="+databaseURL)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("audit command failed: %v; stderr=%q", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("successful audit stderr = %q, want empty", stderr.String())
	}
	for _, secret := range fixture.secrets {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Errorf("audit output contains sensitive fixture marker %q", secret)
		}
	}

	var output struct {
		SessionID string `json:"sessionId"`
		Status    string `json:"status"`
		Events    []struct {
			Type       string          `json:"type"`
			OccurredAt string          `json:"occurredAt"`
			Metadata   json.RawMessage `json:"metadata"`
		} `json:"events"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("decode audit stdout as one JSON object: %v; stdout=%q", err, stdout.String())
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("audit stdout must contain exactly one JSON object; trailing decode error = %v", err)
	}
	if output.SessionID != fixture.sessionID.String() || output.Status != "expired" {
		t.Errorf("audit identity/state = %q/%q, want %s/expired", output.SessionID, output.Status, fixture.sessionID)
	}
	if len(output.Events) != 2 {
		t.Fatalf("audit events = %d, want 2 ordered events", len(output.Events))
	}
	if output.Events[0].Type != "submit_personal_details" ||
		output.Events[0].OccurredAt != fixture.firstAt.Format(time.RFC3339) ||
		!isEmptyMetadata(output.Events[0].Metadata) ||
		output.Events[1].Type != "expire" ||
		output.Events[1].OccurredAt != fixture.secondAt.Format(time.RFC3339) ||
		!isEmptyMetadata(output.Events[1].Metadata) {
		t.Errorf("audit events = %#v, want safe chronological history", output.Events)
	}

	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &topLevel); err != nil {
		t.Fatalf("decode audit top-level fields: %v", err)
	}
	if !hasExactJSONKeys(topLevel, "sessionId", "status", "events") {
		t.Errorf("audit top-level fields = %v, want exactly sessionId, status, events", keys(topLevel))
	}
	var eventFields []map[string]json.RawMessage
	if err := json.Unmarshal(topLevel["events"], &eventFields); err != nil {
		t.Fatalf("decode raw event fields: %v", err)
	}
	for _, fields := range eventFields {
		if !hasExactJSONKeys(fields, "type", "occurredAt", "metadata") {
			t.Errorf("audit event fields = %v, want exactly type, occurredAt, metadata", keys(fields))
		}
	}
}

type sensitiveAuditFixture struct {
	sessionID uuid.UUID
	firstAt   time.Time
	secondAt  time.Time
	secrets   []string
}

func seedSensitiveAuditFixture(t *testing.T, ctx context.Context, database *pgx.Conn) sensitiveAuditFixture {
	t.Helper()
	fixture := sensitiveAuditFixture{
		sessionID: uuid.MustParse("321d2a4b-3059-4d67-9fd8-c2869d752150"),
		firstAt:   time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC),
		secondAt:  time.Date(2026, 10, 9, 10, 30, 0, 0, time.UTC),
		secrets: []string{
			"AUDIT_TOKEN_HASH_MARKER", "AUDIT_PERSON_NAME_MARKER",
			"AUDIT_IDENTITY_MARKER", "AUDIT_ADDRESS_MARKER",
			"AUDIT_STORAGE_KEY_MARKER", "AUDIT_PROVIDER_PAYLOAD_MARKER",
		},
	}
	_, err := database.Exec(ctx, `
		INSERT INTO verification_sessions
			(id, status, resume_token_hash, expires_at, created_at, updated_at)
		VALUES ($1, 'expired', convert_to($2, 'UTF8'), $3, $4, $3)
	`, fixture.sessionID, "AUDIT_TOKEN_HASH_MARKER_32_BYTES", fixture.secondAt, fixture.firstAt)
	if err != nil {
		t.Fatalf("seed audit Verification Session: %v", err)
	}
	_, err = database.Exec(ctx, `
		INSERT INTO personal_details
			(verification_session_id, full_name, date_of_birth, identity_number, address)
		VALUES ($1, $2, DATE '1990-01-02', $3, $4)
	`, fixture.sessionID, fixture.secrets[1], fixture.secrets[2], fixture.secrets[3])
	if err != nil {
		t.Fatalf("seed audit Personal Details: %v", err)
	}
	_, err = database.Exec(ctx, `
		INSERT INTO upload_intents
			(id, verification_session_id, kind, storage_key, status, expires_at)
		VALUES ($1, $2, 'identity_document', $3, 'expired', $4)
	`, uuid.New(), fixture.sessionID, fixture.secrets[4], fixture.secondAt)
	if err != nil {
		t.Fatalf("seed audit Upload Intent: %v", err)
	}
	_, err = database.Exec(ctx, `
		INSERT INTO webhook_events
			(id, reported_session_id, payload, received_at)
		VALUES ($1, $2, convert_to($3, 'UTF8'), $4)
	`, uuid.New(), fixture.sessionID, fixture.secrets[5], fixture.secondAt)
	if err != nil {
		t.Fatalf("seed audit Webhook Event: %v", err)
	}
	_, err = database.Exec(ctx, `
		INSERT INTO session_events (id, session_id, event_type, metadata, occurred_at)
		VALUES
			($1, $3, 'submit_personal_details', '{}'::jsonb, $4),
			($2, $3, 'expire', '{}'::jsonb, $5)
	`, uuid.New(), uuid.New(), fixture.sessionID, fixture.firstAt, fixture.secondAt)
	if err != nil {
		t.Fatalf("seed audit Session Events: %v", err)
	}
	return fixture
}

func keys(value map[string]json.RawMessage) []string {
	result := make([]string, 0, len(value))
	for key := range value {
		result = append(result, key)
	}
	return result
}

func hasExactJSONKeys(value map[string]json.RawMessage, names ...string) bool {
	if len(value) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := value[name]; !ok {
			return false
		}
	}
	return true
}

func isEmptyMetadata(raw json.RawMessage) bool {
	var metadata map[string]json.RawMessage
	return json.Unmarshal(raw, &metadata) == nil && metadata != nil && len(metadata) == 0
}
