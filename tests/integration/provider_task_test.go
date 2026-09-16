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
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/adapter/providerhttp"
	"github.com/santosidauruk/lawang-go/internal/adapter/queue"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const (
	providerTaskHarnessTimeout    = 3 * time.Second
	providerProcessHarnessTimeout = 20 * time.Second
)

// TestProviderTaskLoadsImmutableRecordsAndSubmitsExactRequest is the agent-owned
// success tracer for Issue 009 Checkpoint 7, Bagian user points 1-8.
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

// TestProviderWorkerRelaysDurableOutboxToFakeProvider proves the Checkpoint 7
// process boundary: one worker lifecycle relays a durable identifier-only outbox
// task through Redis/Asynq to the real fake-provider handler. Provider
// acknowledgement alone must not apply a verdict to the Verification Session.
func TestProviderWorkerRelaysDurableOutboxToFakeProvider(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)

	callbackRecorder := newCallbackRecorder(t)
	fixture.callbackURL = callbackRecorder.URL() + "/webhooks/verification"
	scenarios := providerhttp.NewScenarioStore()
	fakeProviderService := fakeprovider.NewService(
		scenarios,
		providerhttp.NewCallbackSender(fakeProviderWebhookSecret),
		providerTaskHarnessTimeout,
		nil,
		nil,
	)
	fakeProvider := newProviderHTTPHarness(
		t,
		httpapi.NewFakeProviderScenarioHandler(fakeProviderService, scenarios),
	)
	fakeProviderStopped := false
	t.Cleanup(func() {
		if fakeProviderStopped {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), providerProcessHarnessTimeout)
		defer cancel()
		if err := fakeProviderService.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown fake provider: %v", err)
		}
	})

	outboxID := uuid.MustParse("71368e37-9ed7-49f1-98c6-363b0e44a287")
	if _, err := fixture.database.Exec(fixture.ctx, `
		INSERT INTO outbox (
			id, verification_session_id, task_type, payload, created_at
		) VALUES (
			$1, $2, 'provider:submit',
			jsonb_build_object('sessionId', $2::uuid), $3
		)
	`, outboxID, fixture.sessionID, fixture.now); err != nil {
		t.Fatalf("seed durable provider outbox: %v", err)
	}

	workerBinary := filepath.Join(t.TempDir(), "lawang-worker")
	buildWorker := exec.CommandContext(
		fixture.ctx,
		"go",
		"build",
		"-o",
		workerBinary,
		"../../cmd/worker",
	)
	if output, err := buildWorker.CombinedOutput(); err != nil {
		t.Fatalf("build worker process: %v: %s", err, output)
	}

	workerProcess := exec.Command(workerBinary)
	workerProcess.Env = append(os.Environ(),
		"DATABASE_URL="+fixture.databaseURL,
		"REDIS_ADDRESS="+fixture.redisAddress,
		"REDIS_PASSWORD=",
		"REDIS_DATABASE=0",
		"PROVIDER_BASE_URL="+fakeProvider.URL(),
		"PROVIDER_CALLBACK_URL="+fixture.callbackURL,
		"PROVIDER_TIMEOUT=3s",
		"PROVIDER_WEBHOOK_SECRET="+fakeProviderWebhookSecret,
		"WORKER_CONCURRENCY=1",
		"RELAY_INTERVAL=10ms",
		"OUTBOX_CLAIM_LEASE=1m",
		"SHUTDOWN_TIMEOUT=3s",
		"LOG_LEVEL=error",
	)
	var workerOutput bytes.Buffer
	workerProcess.Stdout = &workerOutput
	workerProcess.Stderr = &workerOutput
	if err := workerProcess.Start(); err != nil {
		t.Fatalf("start worker process: %v", err)
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- workerProcess.Wait() }()
	workerStopped := false
	t.Cleanup(func() {
		if workerStopped {
			return
		}
		_ = workerProcess.Process.Kill()
		select {
		case <-workerDone:
		case <-time.After(providerProcessHarnessTimeout):
		}
	})

	callbackCtx, cancel := context.WithTimeout(
		context.Background(),
		providerProcessHarnessTimeout,
	)
	defer cancel()
	callback, err := callbackRecorder.Next(callbackCtx)
	if err != nil {
		t.Fatalf("fake provider did not acknowledge and callback: %v", err)
	}
	assertExactSignedVerifiedCallback(
		t,
		callback,
		fixture.sessionID,
		fakeProviderWebhookSecret,
	)

	if err := workerProcess.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal worker shutdown: %v", err)
	}
	select {
	case err := <-workerDone:
		if err != nil {
			t.Fatalf("worker process shutdown: %v: %s", err, workerOutput.String())
		}
		workerStopped = true
	case <-time.After(providerProcessHarnessTimeout):
		_ = workerProcess.Process.Kill()
		<-workerDone
		workerStopped = true
		t.Fatalf("worker process did not stop before deadline: %s", workerOutput.String())
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(),
		providerProcessHarnessTimeout,
	)
	defer shutdownCancel()
	if err := fakeProviderService.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("drain fake-provider callback: %v", err)
	}
	fakeProviderStopped = true

	var status string
	var published bool
	var verdictEvents int
	if err := fixture.database.QueryRow(fixture.ctx, `
		SELECT
			vs.status,
			o.published_at IS NOT NULL,
			(
				SELECT count(*)
				FROM session_events
				WHERE session_id = vs.id
				  AND event_type IN ('verification_passed', 'verification_failed')
			)
		FROM verification_sessions vs
		JOIN outbox o ON o.verification_session_id = vs.id
		WHERE vs.id = $1 AND o.id = $2
	`, fixture.sessionID, outboxID).Scan(
		&status,
		&published,
		&verdictEvents,
	); err != nil {
		t.Fatalf("inspect provider acknowledgement outcome: %v", err)
	}
	if status != "verification_pending" || !published || verdictEvents != 0 {
		t.Fatalf(
			"provider acknowledgement = status:%s published:%v verdict-events:%d",
			status,
			published,
			verdictEvents,
		)
	}
}

func TestProviderTaskMissingImmutableRecordIsPermanentAndDoesNotCallProvider(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)
	if _, err := fixture.database.Exec(fixture.ctx, `
		DELETE FROM verification_artifacts
		WHERE verification_session_id = $1 AND kind = 'biometric_capture'
	`, fixture.sessionID); err != nil {
		t.Fatalf("remove required immutable record: %v", err)
	}

	payload, err := json.Marshal(map[string]uuid.UUID{"sessionId": fixture.sessionID})
	if err != nil {
		t.Fatalf("marshal task payload: %v", err)
	}
	err = newFirstProviderTaskHandler(t, fixture).ProcessTask(
		fixture.ctx,
		asynq.NewTask("provider:submit", payload),
	)
	if !errors.Is(err, providersubmission.ErrImmutableRecordsMissing) || !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected permanent missing-record error, got %v", err)
	}
	if got := fixture.provider.Count(); got != 0 {
		t.Fatalf("provider must not be called for incomplete records, got %d requests", got)
	}
}

func TestProviderTaskRepeatedExecutionKeepsStableIdempotencyKey(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)
	payload, err := json.Marshal(map[string]uuid.UUID{"sessionId": fixture.sessionID})
	if err != nil {
		t.Fatalf("marshal task payload: %v", err)
	}
	task := asynq.NewTask("provider:submit", payload)
	handler := newFirstProviderTaskHandler(t, fixture)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := handler.ProcessTask(fixture.ctx, task); err != nil {
			t.Fatalf("provider task attempt %d: %v", attempt, err)
		}
		request, err := fixture.provider.Next(fixture.ctx)
		if err != nil {
			t.Fatalf("read provider request %d: %v", attempt, err)
		}
		if got := request.Header.Get("Idempotency-Key"); got != fixture.sessionID.String() {
			t.Fatalf("attempt %d key = %q, want %q", attempt, got, fixture.sessionID)
		}
	}
}

func TestProviderTaskExhaustsAfterExactlyTenAttemptsWithoutChangingSession(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)

	var providerMu sync.Mutex
	var idempotencyKeys []string
	providerAttempted := make(chan struct{}, queue.MaxProviderRetries+1)
	failingProvider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerMu.Lock()
		idempotencyKeys = append(idempotencyKeys, request.Header.Get("Idempotency-Key"))
		providerMu.Unlock()
		providerAttempted <- struct{}{}
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failingProvider.Close()
	fixture.provider.server.Close()
	fixture.provider.server = failingProvider

	handler := newFirstProviderTaskHandler(t, fixture)
	worker := asynq.NewServer(
		asynq.RedisClientOpt{Addr: fixture.redisAddress},
		asynq.Config{
			Concurrency:              1,
			TaskCheckInterval:        10 * time.Millisecond,
			DelayedTaskCheckInterval: 10 * time.Millisecond,
			RetryDelayFunc: func(int, error, *asynq.Task) time.Duration {
				return 0
			},
		},
	)
	mux := asynq.NewServeMux()
	mux.Handle("provider:submit", handler)
	if err := worker.Start(mux); err != nil {
		t.Fatalf("start exhaustion worker: %v", err)
	}
	t.Cleanup(worker.Shutdown)
	enqueueFirstProviderTask(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for attempt := 1; attempt <= queue.MaxProviderRetries+1; attempt++ {
		select {
		case <-providerAttempted:
		case <-ctx.Done():
			t.Fatalf("waiting for provider attempt %d: %v", attempt, ctx.Err())
		}
	}

	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: fixture.redisAddress})
	defer inspector.Close()
	for {
		archived, err := inspector.ListArchivedTasks("default", asynq.PageSize(20))
		if err != nil {
			t.Fatalf("inspect exhausted task: %v", err)
		}
		if len(archived) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("task was not archived after retry exhaustion: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	providerMu.Lock()
	gotKeys := append([]string(nil), idempotencyKeys...)
	providerMu.Unlock()
	if len(gotKeys) != queue.MaxProviderRetries+1 {
		t.Fatalf("provider attempts = %d, want exactly %d", len(gotKeys), queue.MaxProviderRetries+1)
	}
	for attempt, key := range gotKeys {
		if key != fixture.sessionID.String() {
			t.Fatalf("attempt %d key = %q, want %q", attempt+1, key, fixture.sessionID)
		}
	}

	assertProviderSessionStillPendingWithoutVerdict(t, fixture)
}

func TestProviderWorkerRestartRecoversActiveTaskWithStableIdempotencyKey(t *testing.T) {
	fixture := openProviderTaskFixture(t)
	seedProviderTaskImmutableRecords(t, fixture)

	firstAttemptStarted := make(chan struct{})
	releaseFirstAttempt := make(chan struct{})
	handler := &recoverActiveTaskHandler{
		next:         newFirstProviderTaskHandler(t, fixture),
		firstStarted: firstAttemptStarted,
		releaseFirst: releaseFirstAttempt,
	}
	newWorker := func(shutdownTimeout time.Duration) *asynq.Server {
		return asynq.NewServer(
			asynq.RedisClientOpt{Addr: fixture.redisAddress},
			asynq.Config{
				Concurrency:              1,
				ShutdownTimeout:          shutdownTimeout,
				TaskCheckInterval:        10 * time.Millisecond,
				DelayedTaskCheckInterval: 10 * time.Millisecond,
				RetryDelayFunc:           queue.ProviderRetryDelay,
			},
		)
	}
	startWorker := func(worker *asynq.Server) {
		t.Helper()
		mux := asynq.NewServeMux()
		mux.Handle("provider:submit", handler)
		if err := worker.Start(mux); err != nil {
			t.Fatalf("start provider worker: %v", err)
		}
	}

	firstWorker := newWorker(50 * time.Millisecond)
	startWorker(firstWorker)
	enqueueFirstProviderTask(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-firstAttemptStarted:
	case <-ctx.Done():
		firstWorker.Shutdown()
		t.Fatalf("first active provider attempt did not start: %v", ctx.Err())
	}
	firstWorker.Shutdown()
	close(releaseFirstAttempt)

	secondWorker := newWorker(time.Second)
	startWorker(secondWorker)
	t.Cleanup(secondWorker.Shutdown)
	request, err := fixture.provider.Next(ctx)
	if err != nil {
		t.Fatalf("replacement worker did not recover active task: %v", err)
	}
	if key := request.Header.Get("Idempotency-Key"); key != fixture.sessionID.String() {
		t.Fatalf("recovered task key = %q, want %q", key, fixture.sessionID)
	}
	assertProviderSessionStillPendingWithoutVerdict(t, fixture)
}

type providerTaskFixture struct {
	ctx             context.Context
	databaseURL     string
	database        *pgxpool.Pool
	redisAddress    string
	provider        *providerSubmissionRecorder
	sessionID       uuid.UUID
	callbackURL     string
	now             time.Time
	identityIntent  uuid.UUID
	biometricIntent uuid.UUID
}

type recoverActiveTaskHandler struct {
	mu           sync.Mutex
	attempts     int
	next         asynq.Handler
	firstStarted chan struct{}
	releaseFirst chan struct{}
}

func (h *recoverActiveTaskHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	h.mu.Lock()
	h.attempts++
	attempt := h.attempts
	h.mu.Unlock()
	if attempt == 1 {
		close(h.firstStarted)
		<-h.releaseFirst
		return nil
	}
	return h.next.ProcessTask(ctx, task)
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
		ctx:             ctx,
		databaseURL:     databaseURL,
		database:        database,
		redisAddress:    parsedRedisURL.Host,
		provider:        provider,
		sessionID:       sessionID,
		callbackURL:     "http://lawang-api:8080/webhooks/verification",
		now:             time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		identityIntent:  uuid.MustParse("1e07d796-9178-4bb3-bd10-400cc9382a2a"),
		biometricIntent: uuid.MustParse("7459ca4a-a6cd-44b9-9364-f90664894baa"),
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

// newFirstProviderTaskHandler composes the production PostgreSQL reader,
// application task service, provider HTTP client, and queue handler for the focused
// tracer. Provider I/O remains outside a PostgreSQL transaction.
func newFirstProviderTaskHandler(t *testing.T, fixture providerTaskFixture) asynq.Handler {
	t.Helper()

	reader := postgres.NewProviderSubmissionReader(fixture.database)
	client, err := providerhttp.NewClient(
		fixture.provider.URL(),
		providerTaskHarnessTimeout,
	)
	if err != nil {
		t.Fatalf("create Checkpoint 7 provider client: %v", err)
	}

	service, err := providersubmission.NewTaskService(reader, client, fixture.callbackURL)
	if err != nil {
		t.Fatalf("create Checkpoint 7 task service: %v", err)
	}
	return queue.NewProviderSubmissionHandler(service)
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

func (r *providerSubmissionRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

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

func assertProviderSessionStillPendingWithoutVerdict(t *testing.T, fixture providerTaskFixture) {
	t.Helper()

	var status string
	var verdictEvents int
	if err := fixture.database.QueryRow(fixture.ctx, `
		SELECT
			vs.status,
			(
				SELECT count(*)
				FROM session_events
				WHERE session_id = vs.id
				  AND event_type IN ('verification_passed', 'verification_failed')
			)
		FROM verification_sessions vs
		WHERE vs.id = $1
	`, fixture.sessionID).Scan(&status, &verdictEvents); err != nil {
		t.Fatalf("inspect session after provider attempts: %v", err)
	}
	if status != "verification_pending" || verdictEvents != 0 {
		t.Fatalf("provider attempts changed session: status=%s verdict-events=%d", status, verdictEvents)
	}
}
