package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/adapter/providerhttp"
	queueadapter "github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/fakeprovider"
	"github.com/santosidauruk/lawang/internal/application/outbox"
	"github.com/santosidauruk/lawang/internal/application/personaldetails"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/providerverdict"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestFullProviderVerificationVerifiedFlowUsesEveryRealBoundary(t *testing.T) {
	harness := newProviderVerificationE2E(t)
	result := harness.runFlow(t, fakeprovider.Scenario{Verdict: fakeprovider.Verified}, true)

	if result.status != "verified" || result.webhookStatus != "applied" {
		t.Fatalf("verified E2E = status:%s webhook:%s", result.status, result.webhookStatus)
	}
	if result.sessionID == uuid.Nil || result.outboxID == uuid.Nil || result.webhookEventID == uuid.Nil {
		t.Fatalf(
			"trace IDs = session:%s outbox:%s webhook:%s, want all non-zero",
			result.sessionID,
			result.outboxID,
			result.webhookEventID,
		)
	}
	t.Logf(
		"trace IDs: Verification Session=%s outbox/TaskID=%s provider Idempotency-Key=%s Webhook Event=%s",
		result.sessionID,
		result.outboxID,
		result.sessionID,
		result.webhookEventID,
	)
}

func TestFullProviderVerificationRejectedFlowCoversBoundedReasons(t *testing.T) {
	harness := newProviderVerificationE2E(t)

	for _, reason := range []fakeprovider.RejectionReason{
		fakeprovider.DocumentInvalid,
		fakeprovider.BiometricMismatch,
		fakeprovider.IdentityNotVerified,
		fakeprovider.SuspectedFraud,
	} {
		t.Run(string(reason), func(t *testing.T) {
			result := harness.runFlow(t, fakeprovider.Scenario{
				Verdict: fakeprovider.Rejected,
				Reason:  reason,
			}, false)

			if result.status != "rejected" ||
				result.rejectionReason != string(reason) ||
				result.webhookStatus != "applied" {
				t.Fatalf(
					"rejected E2E = status:%s reason:%s webhook:%s, want rejected/%s/applied",
					result.status,
					result.rejectionReason,
					result.webhookStatus,
					reason,
				)
			}
		})
	}
}

func TestDuplicateCallbacksUnderFullLoopHaveOneIdempotentDomainEffect(t *testing.T) {
	harness := newProviderVerificationE2E(t)
	result := harness.runFlow(t, fakeprovider.Scenario{
		Verdict:            fakeprovider.Verified,
		DuplicateCallbacks: 5,
	}, false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := harness.providerSvc.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("wait for duplicate provider callbacks: %v", err)
	}

	var webhookCount, terminalEventCount int
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT count(*) FROM webhook_events WHERE reported_session_id = $1`,
		result.sessionID,
	).Scan(&webhookCount); err != nil {
		t.Fatalf("count duplicate Webhook Events: %v", err)
	}
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'verification_passed'`,
		result.sessionID,
	).Scan(&terminalEventCount); err != nil {
		t.Fatalf("count duplicate terminal Session Events: %v", err)
	}
	if webhookCount != 1 || terminalEventCount != 1 {
		t.Fatalf(
			"duplicate full-loop effects = webhooks:%d terminal-events:%d, want 1/1",
			webhookCount,
			terminalEventCount,
		)
	}
}

func TestInvalidSignatureInFullHarnessCannotMutateSessionOrWebhookEvents(t *testing.T) {
	harness := newProviderVerificationE2E(t)
	sessionID, _ := harness.createReadySession(t)
	body := []byte(fmt.Sprintf(
		`{"eventId":%q,"sessionId":%q,"verdict":"verified"}`,
		uuid.New(),
		sessionID,
	))
	request, err := http.NewRequest(
		http.MethodPost,
		harness.api.URL+"/webhooks/verification",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("construct invalid-signature request: %v", err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-signature", "sha256="+string(bytes.Repeat([]byte{'0'}, 64)))

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send invalid-signature request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		rawResponse, _ := io.ReadAll(response.Body)
		t.Fatalf("invalid-signature response = %d/%s, want 401", response.StatusCode, rawResponse)
	}

	var status string
	var webhookCount int
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT status FROM verification_sessions WHERE id = $1`,
		sessionID,
	).Scan(&status); err != nil {
		t.Fatalf("read session after invalid signature: %v", err)
	}
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT count(*) FROM webhook_events WHERE reported_session_id = $1`,
		sessionID,
	).Scan(&webhookCount); err != nil {
		t.Fatalf("count Webhook Events after invalid signature: %v", err)
	}
	if status != "biometric_capture_uploaded" || webhookCount != 0 {
		t.Fatalf(
			"invalid-signature effects = status:%s webhooks:%d, want biometric_capture_uploaded/0",
			status,
			webhookCount,
		)
	}
}

func TestRedisRestartAfterDurableSubmitEventuallyProcessesUnpublishedWork(t *testing.T) {
	harness := newProviderVerificationE2E(t)
	sessionID, token := harness.createReadySession(t)
	harness.scenarios.Set(sessionID, fakeprovider.Scenario{Verdict: fakeprovider.Verified})

	response := harness.postJSON(t, "/verification-sessions/"+sessionID.String()+"/submit", token, nil)
	if response.status != http.StatusAccepted {
		t.Fatalf("submit before Redis outage = %d/%s", response.status, response.body)
	}

	stopTimeout := time.Second
	if err := harness.redis.Stop(harness.ctx, &stopTimeout); err != nil {
		t.Fatalf("stop real Redis: %v", err)
	}
	if err := harness.relay.RunOnce(harness.ctx); err == nil {
		t.Fatal("relay while Redis stopped error = nil")
	}

	var status string
	var published bool
	var attempts int
	if err := harness.database.QueryRow(harness.ctx, `
		SELECT vs.status, o.published_at IS NOT NULL, o.attempt_count
		FROM verification_sessions vs
		JOIN outbox o ON o.verification_session_id = vs.id
		WHERE vs.id = $1
	`, sessionID).Scan(&status, &published, &attempts); err != nil {
		t.Fatalf("read durable Redis-outage state: %v", err)
	}
	if status != "verification_pending" || published || attempts != 1 {
		t.Fatalf("Redis outage outcome = status:%s published:%v attempts:%d", status, published, attempts)
	}

	if err := harness.redis.Start(harness.ctx); err != nil {
		t.Fatalf("restart real Redis: %v", err)
	}
	if err := harness.publisher.Close(); err != nil {
		t.Fatalf("close stale Redis publisher: %v", err)
	}
	redisURL, err := harness.redis.ConnectionString(harness.ctx)
	if err != nil {
		t.Fatalf("read restarted Redis endpoint: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse restarted Redis endpoint: %v", err)
	}
	harness.redisAddress = parsedRedisURL.Host
	harness.publisher = queueadapter.NewPublisherForRedis(queueadapter.RedisConfig{Address: harness.redisAddress})
	harness.worker = harness.newWorkerServer()

	recoveryRelay := outbox.NewRelay(
		postgresadapter.NewOutboxStore(harness.database),
		harness.publisher,
		fixedClock{now: harness.now.Add(time.Minute)},
		time.Minute,
	)
	if err := recoveryRelay.RunOnce(harness.ctx); err != nil {
		t.Fatalf("relay after Redis restart: %v", err)
	}
	harness.startWorker(t)
	result := harness.waitForTerminalOutcome(t, sessionID)
	if result.status != "verified" {
		t.Fatalf("recovered Redis flow status = %s", result.status)
	}
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT published_at IS NOT NULL, attempt_count FROM outbox WHERE verification_session_id = $1`,
		sessionID,
	).Scan(&published, &attempts); err != nil {
		t.Fatalf("read recovered outbox: %v", err)
	}
	if !published || attempts != 2 {
		t.Fatalf("recovered outbox = published:%v attempts:%d", published, attempts)
	}
}

func TestProviderAndWorkerRestartRecoverRetryWithStableIdempotencyKey(t *testing.T) {
	harness := newProviderVerificationE2E(t)
	harness.setProviderOutage(true)
	sessionID, token := harness.createReadySession(t)
	harness.scenarios.Set(sessionID, fakeprovider.Scenario{Verdict: fakeprovider.Verified})

	response := harness.postJSON(t, "/verification-sessions/"+sessionID.String()+"/submit", token, nil)
	if response.status != http.StatusAccepted {
		t.Fatalf("submit before provider outage = %d/%s", response.status, response.body)
	}
	if err := harness.relay.RunOnce(harness.ctx); err != nil {
		t.Fatalf("relay provider-restart task: %v", err)
	}
	harness.startWorker(t)

	select {
	case attemptedID := <-harness.providerAttempt:
		if attemptedID != sessionID {
			t.Fatalf("failed provider attempt session = %s, want %s", attemptedID, sessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not reach unavailable provider")
	}

	var pendingStatus string
	if err := harness.database.QueryRow(
		harness.ctx,
		`SELECT status FROM verification_sessions WHERE id = $1`,
		sessionID,
	).Scan(&pendingStatus); err != nil {
		t.Fatalf("read session after transient provider failure: %v", err)
	}
	if pendingStatus != "verification_pending" {
		t.Fatalf("transient provider failure changed session to %s", pendingStatus)
	}

	harness.stopWorker()
	harness.restartProviderOnSameAddress(t)
	harness.setProviderOutage(false)
	harness.restartWorker(t)

	result := harness.waitForTerminalOutcome(t, sessionID)
	if result.status != "verified" {
		t.Fatalf("provider/worker restart outcome = %s", result.status)
	}
	harness.keyMu.Lock()
	keys := append([]string(nil), harness.providerKeys[sessionID]...)
	harness.keyMu.Unlock()
	if len(keys) < 2 {
		t.Fatalf("provider attempts = %d, want failed and recovered attempts", len(keys))
	}
	for _, key := range keys {
		if key != sessionID.String() {
			t.Fatalf("provider retry key = %q, want stable %s", key, sessionID)
		}
	}
}

type providerVerificationE2E struct {
	ctx             context.Context
	database        *pgxpool.Pool
	api             *httptest.Server
	provider        *httptest.Server
	providerSvc     *fakeprovider.Service
	scenarios       *providerhttp.ScenarioStore
	extractions     map[string]artifact.DocumentExtraction
	redisAddress    string
	redis           *tcredis.RedisContainer
	publisher       *queueadapter.Publisher
	relay           *outbox.Relay
	worker          *queueadapter.Server
	taskService     *providersubmission.TaskService
	expiryService   *expiry.SweepService
	cleanupService  *cleanup.Service
	workerMu        sync.Mutex
	workerStarted   bool
	now             time.Time
	keyMu           sync.Mutex
	providerKeys    map[uuid.UUID][]string
	providerHandler http.Handler
	providerOutage  bool
	providerAttempt chan uuid.UUID
}

type providerVerificationResult struct {
	sessionID       uuid.UUID
	outboxID        uuid.UUID
	webhookEventID  uuid.UUID
	status          string
	rejectionReason string
	webhookStatus   string
}

func newProviderVerificationE2E(t *testing.T) *providerVerificationE2E {
	t.Helper()
	ctx, connection := openProviderVerdictDatabase(t)
	database, err := pgxpool.New(ctx, connection.Config().ConnString())
	if err != nil {
		t.Fatalf("create E2E PostgreSQL pool: %v", err)
	}
	t.Cleanup(database.Close)

	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start E2E Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("read E2E Redis connection string: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse E2E Redis connection string: %v", err)
	}

	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	tokens := session.NewProductionCryptoTokens()
	storage := newMinIOTestStorage(t, ctx)
	cleanupService := cleanup.NewService(
		postgresadapter.NewCleanupRepository(database),
		storage,
		fixedClock{now: now},
	)
	extractions := map[string]artifact.DocumentExtraction{}
	artifacts := postgresadapter.NewArtifactTransactions(database)
	artifactService := artifact.NewService(
		artifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(database)),
		storage,
		deterministicextractor.New(extractions, nil),
		tokens,
		fixedClock{now: now}, nil)

	uploadIntents := artifact.NewUploadIntentService(artifacts, artifacts, storage, tokens, fixedClock{now: now}, nil)
	verdictService := providerverdict.NewProviderVerdictService(
		fixedClock{now: now},
		postgresadapter.NewProviderVerdictTransactions(database),
	)
	apiHandler := httpapi.NewHandler(
		session.NewService(postgresadapter.NewSessionStore(database), tokens, fixedClock{now: now}, nil),
		personaldetails.NewService(postgresadapter.NewPersonalDetailsTransactions(database), tokens, fixedClock{now: now}, nil),
		artifactService,
		uploadIntents,
		&httpapi.VerifiedBody{Service: verdictService, ProviderWebhookSecret: checkpoint5WebhookSecret},
		providersubmission.NewService(postgresadapter.NewProviderSubmissionTransactions(database), tokens, fixedClock{now: now}, nil),
	)
	apiServer := httptest.NewServer(apiHandler)
	t.Cleanup(apiServer.Close)

	scenarios := providerhttp.NewScenarioStore()
	providerService := fakeprovider.NewService(
		scenarios,
		providerhttp.NewCallbackSender(checkpoint5WebhookSecret),
		3*time.Second,
		nil,
		nil,
	)
	harness := &providerVerificationE2E{
		ctx:             ctx,
		database:        database,
		api:             apiServer,
		providerSvc:     providerService,
		scenarios:       scenarios,
		extractions:     extractions,
		redisAddress:    parsedRedisURL.Host,
		redis:           redisContainer,
		cleanupService:  cleanupService,
		now:             now,
		providerKeys:    map[uuid.UUID][]string{},
		providerAttempt: make(chan uuid.UUID, 1),
	}
	providerHandler := httpapi.NewFakeProviderScenarioHandler(providerService, scenarios)
	harness.providerHandler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if raw := request.Header.Get("Idempotency-Key"); raw != "" {
			if id, parseErr := uuid.Parse(raw); parseErr == nil {
				harness.keyMu.Lock()
				harness.providerKeys[id] = append(harness.providerKeys[id], raw)
				outage := harness.providerOutage
				harness.keyMu.Unlock()
				if outage {
					select {
					case harness.providerAttempt <- id:
					default:
					}
					response.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
		}
		providerHandler.ServeHTTP(response, request)
	})
	harness.provider = httptest.NewServer(harness.providerHandler)
	t.Cleanup(harness.provider.Close)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := providerService.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown E2E fake provider: %v", err)
		}
	})

	providerClient, err := providerhttp.NewClient(harness.provider.URL+"/", time.Second)
	if err != nil {
		t.Fatalf("construct E2E provider client: %v", err)
	}
	taskService, err := providersubmission.NewTaskService(
		postgresadapter.NewProviderSubmissionReader(database),
		providerClient,
		apiServer.URL+"/webhooks/verification",
	)
	if err != nil {
		t.Fatalf("construct E2E provider task service: %v", err)
	}
	harness.taskService = taskService
	redisConfig := queueadapter.RedisConfig{Address: harness.redisAddress}
	harness.publisher = queueadapter.NewPublisherForRedis(redisConfig)
	t.Cleanup(func() { _ = harness.publisher.Close() })
	harness.relay = outbox.NewRelay(
		postgresadapter.NewOutboxStore(database),
		harness.publisher,
		fixedClock{now: now},
		time.Minute,
	)
	harness.worker = harness.newWorkerServer()
	t.Cleanup(func() {
		harness.workerMu.Lock()
		defer harness.workerMu.Unlock()
		if harness.workerStarted {
			harness.worker.Shutdown()
			harness.workerStarted = false
		}
	})
	return harness
}

func (h *providerVerificationE2E) runFlow(t *testing.T, scenario fakeprovider.Scenario, inspectTask bool) providerVerificationResult {
	t.Helper()
	sessionID, rawToken := h.createReadySession(t)
	h.scenarios.Set(sessionID, scenario)

	response := h.postJSON(t, "/verification-sessions/"+sessionID.String()+"/submit", rawToken, nil)
	if response.status != http.StatusAccepted || string(response.body) != fmt.Sprintf("{\"id\":\"%s\",\"status\":\"verification_pending\"}\n", sessionID) {
		t.Fatalf("submit = %d/%s", response.status, response.body)
	}
	var outboxID uuid.UUID
	if err := h.database.QueryRow(h.ctx, `SELECT id FROM outbox WHERE verification_session_id = $1`, sessionID).Scan(&outboxID); err != nil {
		t.Fatalf("read durable outbox ID: %v", err)
	}
	if err := h.relay.RunOnce(h.ctx); err != nil {
		t.Fatalf("relay E2E outbox: %v", err)
	}
	if inspectTask {
		h.assertPendingTaskMatchesOutbox(t, outboxID, sessionID)
	}
	h.startWorker(t)

	result := h.waitForTerminalOutcome(t, sessionID)
	result.outboxID = outboxID
	h.keyMu.Lock()
	keys := append([]string(nil), h.providerKeys[sessionID]...)
	h.keyMu.Unlock()
	if len(keys) == 0 {
		t.Fatal("provider did not receive submission")
	}
	for _, key := range keys {
		if key != sessionID.String() {
			t.Fatalf("provider Idempotency-Key = %q, want %s", key, sessionID)
		}
	}
	return result
}

func (h *providerVerificationE2E) newWorkerServer() *queueadapter.Server {
	return queueadapter.NewAsynqTaskServer(
		queueadapter.RedisConfig{Address: h.redisAddress},
		2,
		time.Second,
		h.taskService,
		h.expiryService,
		h.cleanupService,
		10,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func (h *providerVerificationE2E) startWorker(t *testing.T) {
	t.Helper()
	h.workerMu.Lock()
	defer h.workerMu.Unlock()
	if h.workerStarted {
		return
	}
	if err := h.worker.Start(); err != nil {
		t.Fatalf("start E2E worker: %v", err)
	}
	h.workerStarted = true
}

func (h *providerVerificationE2E) stopWorker() {
	h.workerMu.Lock()
	defer h.workerMu.Unlock()
	if !h.workerStarted {
		return
	}
	h.worker.Shutdown()
	h.workerStarted = false
}

func (h *providerVerificationE2E) restartWorker(t *testing.T) {
	t.Helper()
	h.workerMu.Lock()
	h.worker = h.newWorkerServer()
	h.workerStarted = false
	h.workerMu.Unlock()
	h.startWorker(t)
}

func (h *providerVerificationE2E) setProviderOutage(outage bool) {
	h.keyMu.Lock()
	h.providerOutage = outage
	h.keyMu.Unlock()
}

func (h *providerVerificationE2E) restartProviderOnSameAddress(t *testing.T) {
	t.Helper()
	address := h.provider.Listener.Addr().String()
	h.provider.Close()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("restart provider listener on %s: %v", address, err)
	}
	restarted := httptest.NewUnstartedServer(h.providerHandler)
	restarted.Listener = listener
	restarted.Start()
	h.provider = restarted
	t.Cleanup(restarted.Close)
}

func (h *providerVerificationE2E) createReadySession(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	created := h.postJSON(t, "/verification-sessions", "", nil)
	if created.status != http.StatusCreated {
		t.Fatalf("create session = %d/%s", created.status, created.body)
	}
	var sessionBody struct {
		ID          uuid.UUID `json:"id"`
		ResumeToken string    `json:"resumeToken"`
	}
	if err := json.Unmarshal(created.body, &sessionBody); err != nil {
		t.Fatalf("decode created session: %v", err)
	}
	details := []byte(`{"fullName":"E2E Applicant","dateOfBirth":"2000-01-01","identityNumber":"3173000000000008","address":"jalan full asynchronous loop"}`)
	if response := h.postJSON(t, "/verification-sessions/"+sessionBody.ID.String()+"/personal-details", sessionBody.ResumeToken, details); response.status != http.StatusOK {
		t.Fatalf("personal details = %d/%s", response.status, response.body)
	}
	h.uploadAndConfirm(t, sessionBody.ID, sessionBody.ResumeToken, "identity_document", "image/jpeg", smallJPEG(t), "3173000000000008")
	h.uploadAndConfirm(t, sessionBody.ID, sessionBody.ResumeToken, "biometric_capture", "image/png", smallPNG(t), "")
	return sessionBody.ID, sessionBody.ResumeToken
}

func (h *providerVerificationE2E) uploadAndConfirm(t *testing.T, sessionID uuid.UUID, token, kind, contentType string, body []byte, extractedIdentity string) {
	t.Helper()
	upload := h.postJSON(t, "/verification-sessions/"+sessionID.String()+"/artifacts/upload-url", token, []byte(`{"kind":"`+kind+`"}`))
	if upload.status != http.StatusCreated {
		t.Fatalf("create %s Upload Intent = %d/%s", kind, upload.status, upload.body)
	}
	var created struct {
		UploadIntentID uuid.UUID `json:"uploadIntentId"`
		UploadURL      string    `json:"uploadUrl"`
	}
	if err := json.Unmarshal(upload.body, &created); err != nil {
		t.Fatalf("decode %s Upload Intent: %v", kind, err)
	}
	var storageKey string
	if err := h.database.QueryRow(h.ctx, `SELECT storage_key FROM upload_intents WHERE id = $1`, created.UploadIntentID).Scan(&storageKey); err != nil {
		t.Fatalf("read %s storage key: %v", kind, err)
	}
	if extractedIdentity != "" {
		h.extractions[storageKey] = artifact.DocumentExtraction{IdentityNumber: extractedIdentity}
	}
	request, err := http.NewRequestWithContext(h.ctx, http.MethodPut, created.UploadURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create %s PUT request: %v", kind, err)
	}
	request.Header.Set("Content-Type", contentType)
	put, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("PUT %s object: %v", kind, err)
	}
	putBody, _ := io.ReadAll(io.LimitReader(put.Body, 4096))
	_ = put.Body.Close()
	if put.StatusCode < http.StatusOK || put.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("PUT %s = %d/%s", kind, put.StatusCode, putBody)
	}
	confirmBody := []byte(`{"uploadIntentId":"` + created.UploadIntentID.String() + `"}`)
	if confirmed := h.postJSON(t, "/verification-sessions/"+sessionID.String()+"/artifacts/confirm", token, confirmBody); confirmed.status != http.StatusOK {
		t.Fatalf("confirm %s = %d/%s", kind, confirmed.status, confirmed.body)
	}
}

type e2eHTTPResponse struct {
	status int
	body   []byte
}

func (h *providerVerificationE2E) postJSON(t *testing.T, path, token string, body []byte) e2eHTTPResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.api.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create POST %s: %v", path, err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read POST %s response: %v", path, err)
	}
	return e2eHTTPResponse{status: response.StatusCode, body: responseBody}
}

func (h *providerVerificationE2E) assertPendingTaskMatchesOutbox(t *testing.T, outboxID, sessionID uuid.UUID) {
	t.Helper()
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: h.redisAddress})
	defer inspector.Close()
	tasks, err := inspector.ListPendingTasks("default")
	if err != nil {
		t.Fatalf("inspect pending provider task: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != outboxID.String() || tasks[0].MaxRetry != queueadapter.MaxProviderRetries {
		t.Fatalf("queued task = %#v, want TaskID %s and nine retries", tasks, outboxID)
	}
	var payload struct {
		SessionID uuid.UUID `json:"sessionId"`
	}
	if err := json.Unmarshal(tasks[0].Payload, &payload); err != nil || payload.SessionID != sessionID {
		t.Fatalf("queued payload = %s, want identifier-only session %s", tasks[0].Payload, sessionID)
	}
}

func (h *providerVerificationE2E) waitForTerminalOutcome(t *testing.T, sessionID uuid.UUID) providerVerificationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result providerVerificationResult
		result.sessionID = sessionID
		var rejectionReason *string
		err := h.database.QueryRow(ctx, `
			SELECT vs.status, vs.rejection_reason, we.id, we.processing_status
			FROM verification_sessions vs
			JOIN webhook_events we ON we.reported_session_id = vs.id
			WHERE vs.id = $1 AND we.processing_status = 'applied'
			ORDER BY we.received_at LIMIT 1
		`, sessionID).Scan(&result.status, &rejectionReason, &result.webhookEventID, &result.webhookStatus)
		if err == nil {
			if rejectionReason != nil {
				result.rejectionReason = *rejectionReason
			}
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for terminal E2E outcome: %v (last query: %v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}
