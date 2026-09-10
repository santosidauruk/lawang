package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const providerTaskHarnessTimeout = 3 * time.Second

var errCheckpoint7UserWiringRequired = errors.New("checkpoint 7 user wiring is not connected")

// TestProviderTaskLoadsImmutableRecordsAndSubmitsExactRequest is the agent-owned
// RED success tracer for Issue 009 Checkpoint 7, Bagian user points 1-8.
//
// Keep this as one observable behavior: real Redis hands an identifier-only task to
// the queue adapter, the task service loads immutable records from PostgreSQL at
// execution time, and the provider receives the exact Checkpoint 6 request with the
// session UUID as Idempotency-Key. Retry, exhaustion, cancellation, and malformed
// task siblings remain after the [review] gate.
func TestProviderTaskLoadsImmutableRecordsAndSubmitsExactRequest(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)
	enqueueFirstProviderTask(t, fixture)

	handler := newFirstProviderTaskHandler(t, fixture)
	worker := asynq.NewServer(
		asynq.RedisClientOpt{Addr: fixture.redisAddress},
		asynq.Config{Concurrency: 1},
	)
	mux := asynq.NewServeMux()
	mux.Handle("provider:submit", handler)
	if err := worker.Start(mux); err != nil {
		t.Fatalf("start Checkpoint 7 Asynq worker: %v", err)
	}
	t.Cleanup(worker.Shutdown)

	ctx, cancel := context.WithTimeout(context.Background(), providerTaskHarnessTimeout)
	defer cancel()
	type providerResult struct {
		request recordedProviderSubmission
		err     error
	}
	providerResults := make(chan providerResult, 1)
	go func() {
		request, err := fixture.provider.Next(ctx)
		providerResults <- providerResult{request: request, err: err}
	}()

	var request recordedProviderSubmission
	select {
	case err := <-fixture.userWiringAttempted:
		t.Fatalf("provider task reached intentional user-owned wiring seam: %v", err)
	case result := <-providerResults:
		if result.err != nil {
			t.Fatalf("provider did not receive first task submission: %v", result.err)
		}
		request = result.request
	case <-ctx.Done():
		t.Fatalf("provider task did not run before harness deadline: %v", ctx.Err())
	}

	assertExactProviderTaskRequest(t, request, fixture)
}

type providerTaskFixture struct {
	ctx                 context.Context
	database            *pgxpool.Pool
	redisAddress        string
	provider            *providerSubmissionRecorder
	sessionID           uuid.UUID
	callbackURL         string
	now                 time.Time
	identityIntent      uuid.UUID
	biometricIntent     uuid.UUID
	userWiringAttempted chan error
}

func openProviderTaskFixture(t *testing.T) providerTaskFixture {
	t.Helper()
	ctx, postgresContainer, databaseURL := openUploadIntentDatabase(t)
	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
	} {
		runPSQLFile(t, ctx, postgresContainer, migration.hostPath, migration.containerPath)
	}

	database, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create Checkpoint 7 PostgreSQL pool: %v", err)
	}
	t.Cleanup(database.Close)

	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start disposable Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get disposable Redis connection string: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse disposable Redis connection string: %v", err)
	}

	provider := newProviderSubmissionRecorder(t)
	sessionID := uuid.MustParse("63a6068d-888a-4cac-9f5e-bac2bdb859dd")
	return providerTaskFixture{
		ctx:                 ctx,
		database:            database,
		redisAddress:        parsedRedisURL.Host,
		provider:            provider,
		sessionID:           sessionID,
		callbackURL:         "http://lawang-api:8080/webhooks/verification",
		now:                 time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		identityIntent:      uuid.MustParse("1e07d796-9178-4bb3-bd10-400cc9382a2a"),
		biometricIntent:     uuid.MustParse("7459ca4a-a6cd-44b9-9364-f90664894baa"),
		userWiringAttempted: make(chan error, 1),
	}
}

func seedProviderTaskImmutableRecords(t *testing.T, f providerTaskFixture) {
	t.Helper()
	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO verification_sessions (
			id, resume_token_hash, status, created_at, updated_at, expires_at,
			verification_deadline_at
		) VALUES (
			$1, decode(repeat('42', 32), 'hex'), 'verification_pending',
			$2, $2, $3, $4
		)
	`, f.sessionID, f.now.Add(-time.Hour), f.now.Add(30*time.Minute), f.now.Add(24*time.Hour)); err != nil {
		t.Fatalf("seed pending Verification Session: %v", err)
	}

	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO personal_details (
			verification_session_id, full_name, date_of_birth, identity_number,
			address, created_at
		) VALUES ($1, 'Checkpoint Seven', DATE '2000-01-02', '3173000000000007',
			'jalan task worker', $2)
	`, f.sessionID, f.now.Add(-50*time.Minute)); err != nil {
		t.Fatalf("seed immutable Personal Details: %v", err)
	}

	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status, created_at,
			latest_status_change_at, expires_at, confirmed_at
		) VALUES
			($1, $3, 'identity_document', $5, 'confirmed', $4, $4, $4, $4),
			($2, $3, 'biometric_capture', $6, 'confirmed', $4, $4, $4, $4)
	`,
		f.identityIntent,
		f.biometricIntent,
		f.sessionID,
		f.now.Add(-40*time.Minute),
		"verification-sessions/63a6068d/identity_document/fixed",
		"verification-sessions/63a6068d/biometric_capture/fixed",
	); err != nil {
		t.Fatalf("seed confirmed Upload Intents: %v", err)
	}

	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO verification_artifacts (
			id, upload_intent_id, verification_session_id, kind, storage_key,
			content_type, size_bytes, etag, created_at
		) VALUES
			($1, $3, $5, 'identity_document', $7, 'image/jpeg', 2048,
				'identity-document-etag', $6),
			($2, $4, $5, 'biometric_capture', $8, 'image/png', 1024,
				'biometric-capture-etag', $6)
	`,
		uuid.MustParse("7699058e-82e6-4649-8996-875648528f15"),
		uuid.MustParse("cc620c09-3aa2-441c-a396-b636b90b9c13"),
		f.identityIntent,
		f.biometricIntent,
		f.sessionID,
		f.now.Add(-30*time.Minute),
		"verification-sessions/63a6068d/identity_document/fixed",
		"verification-sessions/63a6068d/biometric_capture/fixed",
	); err != nil {
		t.Fatalf("seed accepted Verification Artifacts: %v", err)
	}
}

func enqueueFirstProviderTask(t *testing.T, f providerTaskFixture) {
	t.Helper()
	payload, err := json.Marshal(struct {
		SessionID uuid.UUID `json:"sessionId"`
	}{SessionID: f.sessionID})
	if err != nil {
		t.Fatalf("marshal identifier-only provider task: %v", err)
	}
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: f.redisAddress})
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.EnqueueContext(
		f.ctx,
		asynq.NewTask("provider:submit", payload),
		asynq.TaskID("71368e37-9ed7-49f1-98c6-363b0e44a287"),
		asynq.MaxRetry(9),
	); err != nil {
		t.Fatalf("enqueue first provider task: %v", err)
	}
}

// newFirstProviderTaskHandler is the only intentional RED seam. Replace this
// test-only construction with the user-authored PostgreSQL reader, application task
// service, provider HTTP client, and queue handler. Do not put production behavior in
// this helper and do not move provider I/O into a PostgreSQL transaction.
func newFirstProviderTaskHandler(t *testing.T, fixture providerTaskFixture) asynq.Handler {
	t.Helper()
	return asynq.HandlerFunc(func(context.Context, *asynq.Task) error {
		select {
		case fixture.userWiringAttempted <- errCheckpoint7UserWiringRequired:
		default:
		}
		return errCheckpoint7UserWiringRequired
	})
}

type recordedProviderSubmission struct {
	Method httpMethod
	Path   string
	Header http.Header
	Body   []byte
}

type providerSubmissionRecorder struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedProviderSubmission
	notify   chan struct{}
}

func newProviderSubmissionRecorder(t *testing.T) *providerSubmissionRecorder {
	t.Helper()
	recorder := &providerSubmissionRecorder{notify: make(chan struct{}, 1)}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read provider request", http.StatusBadRequest)
			return
		}
		recorder.mu.Lock()
		recorder.requests = append(recorder.requests, recordedProviderSubmission{
			Method: httpMethod(r.Method),
			Path:   r.URL.EscapedPath(),
			Header: r.Header.Clone(),
			Body:   append([]byte(nil), body...),
		})
		recorder.mu.Unlock()
		select {
		case recorder.notify <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(recorder.server.Close)
	return recorder
}

func (r *providerSubmissionRecorder) URL() string { return r.server.URL }

func (r *providerSubmissionRecorder) Next(ctx context.Context) (recordedProviderSubmission, error) {
	for {
		r.mu.Lock()
		if len(r.requests) > 0 {
			request := r.requests[0]
			r.requests = r.requests[1:]
			r.mu.Unlock()
			return request, nil
		}
		r.mu.Unlock()
		select {
		case <-r.notify:
		case <-ctx.Done():
			return recordedProviderSubmission{}, ctx.Err()
		}
	}
}

func assertExactProviderTaskRequest(t *testing.T, got recordedProviderSubmission, f providerTaskFixture) {
	t.Helper()
	if got.Method != httpMethod(http.MethodPost) || got.Path != "/" {
		t.Fatalf("provider target = %s %s, want POST /", got.Method, got.Path)
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got.Header.Get("Content-Type"))
	}
	if got.Header.Get("Idempotency-Key") != f.sessionID.String() {
		t.Fatalf("Idempotency-Key = %q, want exact session UUID", got.Header.Get("Idempotency-Key"))
	}
	want := fmt.Sprintf(
		`{"sessionId":"%s","callbackUrl":"%s","personalDetails":{"fullName":"Checkpoint Seven","dateOfBirth":"2000-01-02","identityNumber":"3173000000000007","address":"jalan task worker"},"identityDocument":{"kind":"identity_document","storageKey":"verification-sessions/63a6068d/identity_document/fixed","contentType":"image/jpeg","sizeBytes":2048,"eTag":"identity-document-etag"},"biometricCapture":{"kind":"biometric_capture","storageKey":"verification-sessions/63a6068d/biometric_capture/fixed","contentType":"image/png","sizeBytes":1024,"eTag":"biometric-capture-etag"}}`,
		f.sessionID,
		f.callbackURL,
	)
	if !bytes.Equal(got.Body, []byte(want)) {
		t.Fatalf("provider body = %s, want exact %s", got.Body, want)
	}
}
