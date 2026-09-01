package integration_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/adapter/providerhttp"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
)

const fakeProviderHarnessTimeout = 2 * time.Second
const fakeProviderWebhookSecret = "checkpoint-6-fixed-webhook-secret"

// TestFakeProviderDefaultVerifiedSubmission is the agent-owned RED success tracer
// for the first vertical path through Issue 009 Checkpoint 6, Bagian user steps
// 2-5 and verification step 7. Same-key replay in step 6 gets its own RED only
// after this first path is GREEN.
//
// Keep this as one observable behavior: an exact Provider Submission is
// acknowledged before one deterministic, signed verified callback reaches the
// recorder. Rejection, delay, duplicate, malformed, timeout, and concurrency cases
// remain outside this tracer and are agent-owned only after the [review] gate.
func TestFakeProviderDefaultVerifiedSubmission(t *testing.T) {
	sessionID := uuid.MustParse("fa31360d-910c-4ef3-a31e-9f5b05ed2661")
	callbackRecorder := newBlockingCallbackRecorder(t)
	callbackURL := callbackRecorder.URL() + "/webhooks/verification"
	scenarios := newFixedScenarioStore(fakeprovider.Scenario{Verdict: fakeprovider.Verified})
	handler := newFirstFakeProviderHandler(t, scenarios, fakeProviderWebhookSecret)
	provider := newProviderHTTPHarness(t, handler)

	submission := fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: callbackURL,
		PersonalDetails: fakeprovider.PersonalDetails{
			FullName:       "Checkpoint Six",
			DateOfBirth:    "2000-01-01",
			IdentityNumber: "3173000000000008",
			Address:        "jalan boulevard",
		},
		IdentityDocument: fakeprovider.VerificationArtifactMetadata{
			Kind:        "identity_document",
			StorageKey:  "verification-sessions/fa31360d/identity_document/fixed",
			ContentType: "image/jpeg",
			SizeBytes:   2048,
			ETag:        "identity-document-etag",
		},
		BiometricCapture: fakeprovider.VerificationArtifactMetadata{
			Kind:        "biometric_capture",
			StorageKey:  "verification-sessions/fa31360d/biometric_capture/fixed",
			ContentType: "image/png",
			SizeBytes:   1024,
			ETag:        "biometric-capture-etag",
		},
	}
	submissionBody, err := json.Marshal(submission)
	if err != nil {
		t.Fatalf("marshal exact Provider Submission: %v", err)
	}
	wantSubmissionBody := fmt.Sprintf(
		`{"sessionId":"%s","callbackUrl":"%s","personalDetails":{"fullName":"Checkpoint Six","dateOfBirth":"2000-01-01","identityNumber":"3173000000000008","address":"jalan boulevard"},"identityDocument":{"kind":"identity_document","storageKey":"verification-sessions/fa31360d/identity_document/fixed","contentType":"image/jpeg","sizeBytes":2048,"eTag":"identity-document-etag"},"biometricCapture":{"kind":"biometric_capture","storageKey":"verification-sessions/fa31360d/biometric_capture/fixed","contentType":"image/png","sizeBytes":1024,"eTag":"biometric-capture-etag"}}`,
		sessionID,
		callbackURL,
	)
	if string(submissionBody) != wantSubmissionBody {
		t.Fatalf("Provider Submission JSON does not match the exact named-field contract")
	}

	request, err := http.NewRequest(http.MethodPost, provider.URL(), bytes.NewReader(submissionBody))
	if err != nil {
		t.Fatalf("new Provider Submission request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", sessionID.String())

	type providerResult struct {
		response *http.Response
		err      error
	}
	providerResults := make(chan providerResult, 1)
	go func() {
		response, requestErr := provider.Client().Do(request)
		providerResults <- providerResult{response: response, err: requestErr}
	}()

	callbackCtx, cancel := context.WithTimeout(context.Background(), fakeProviderHarnessTimeout)
	defer cancel()
	callbackResults := make(chan struct {
		callback recordedCallback
		err      error
	}, 1)
	go func() {
		callback, callbackErr := callbackRecorder.Next(callbackCtx)
		callbackResults <- struct {
			callback recordedCallback
			err      error
		}{callback: callback, err: callbackErr}
	}()

	var callback recordedCallback
	gotCallback := false
	gotAcknowledgement := false
	for !gotCallback || !gotAcknowledgement {
		select {
		case result := <-providerResults:
			if result.err != nil {
				t.Fatalf("submit to fake provider: %v", result.err)
			}
			defer result.response.Body.Close()
			if result.response.StatusCode < http.StatusOK || result.response.StatusCode >= http.StatusMultipleChoices {
				t.Fatalf("fake provider acknowledgement status = %d, want 2xx", result.response.StatusCode)
			}
			gotAcknowledgement = true
		case result := <-callbackResults:
			if result.err != nil {
				t.Fatalf("receive default verified callback: %v", result.err)
			}
			callback = result.callback
			gotCallback = true
		case <-callbackCtx.Done():
			t.Fatalf(
				"default verified exchange did not complete while callback response was blocked: %v",
				callbackCtx.Err(),
			)
		}
	}

	assertExactSignedVerifiedCallback(t, callback, sessionID, fakeProviderWebhookSecret)
	callbackRecorder.Release()
}

// TestFakeProviderScenarioEndpointStoresRejectedScenario is the first RED for
// Checkpoint 6, Bagian user step 4. Keep this slice to one valid configuration;
// invalid verdict/reason/delay/duplicate-count cases follow only after it is GREEN.
func TestFakeProviderScenarioEndpointStoresRejectedScenario(t *testing.T) {
	sessionID := uuid.MustParse("23d586eb-71c2-4615-b4fd-78e203f2963b")
	defaultScenario := fakeprovider.Scenario{Verdict: fakeprovider.Verified}
	scenarios := newFixedScenarioStore(defaultScenario)
	handler := newFirstFakeProviderScenarioHandler(t, scenarios)

	request := httptest.NewRequest(
		http.MethodPut,
		"/test/scenarios/"+sessionID.String(),
		strings.NewReader(`{"verdict":"rejected","reason":"document_invalid","delayMs":50,"duplicateCallbacks":1}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		t.Fatalf("scenario acknowledgement status = %d, want 2xx", response.Code)
	}
	want := fakeprovider.Scenario{
		Verdict:            fakeprovider.Rejected,
		Reason:             fakeprovider.DocumentInvalid,
		DelayMs:            50,
		DuplicateCallbacks: 1,
	}
	if got := scenarios.Lookup(sessionID); got != want {
		t.Fatalf("stored scenario = %#v, want %#v", got, want)
	}
}

func TestFakeProviderScenarioEndpointStoresVerifiedScenarioWithoutReason(t *testing.T) {
	sessionID := uuid.MustParse("696a37f8-dba2-4d62-9d75-2b3678928c83")
	defaultScenario := fakeprovider.Scenario{Verdict: fakeprovider.Rejected, Reason: fakeprovider.SuspectedFraud}
	scenarios := newFixedScenarioStore(defaultScenario)
	handler := newFirstFakeProviderScenarioHandler(t, scenarios)

	request := httptest.NewRequest(
		http.MethodPut,
		"/test/scenarios/"+sessionID.String(),
		strings.NewReader(`{"verdict":"verified","delayMs":0,"duplicateCallbacks":0}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		t.Fatalf("scenario acknowledgement status = %d, want 2xx", response.Code)
	}
	want := fakeprovider.Scenario{
		Verdict:            fakeprovider.Verified,
		DelayMs:            0,
		DuplicateCallbacks: 0,
	}
	if got := scenarios.Lookup(sessionID); got != want {
		t.Fatalf("stored scenario = %#v, want %#v", got, want)
	}
}

// newFirstFakeProviderScenarioHandler is the production handoff for Bagian user
// step 4. Replace this deliberate 501 handler with the user-authored provider-only
// router and strict scenario handler; do not register it on the Lawang applicant API.
func newFirstFakeProviderScenarioHandler(
	t *testing.T,
	scenarios *fixedScenarioStore[fakeprovider.Scenario],
) http.Handler {
	t.Helper()
	return httpapi.NewFakeProviderScenarioHandler(nil, scenarios)
}

// newFirstFakeProviderHandler is the only production-construction handoff in this
// agent-owned tracer. Replace this deliberate 501 handler with the user-authored
// scenario store, callback sender, application service, and strict HTTP handler.
func newFirstFakeProviderHandler(
	t *testing.T,
	scenarios *fixedScenarioStore[fakeprovider.Scenario],
	webhookSecret string,
) http.Handler {
	t.Helper()

	callbackSender := providerhttp.NewCallbackSender(webhookSecret)
	service := fakeprovider.NewService(scenarios, callbackSender, fakeProviderHarnessTimeout, nil)
	return httpapi.NewFakeProviderScenarioHandler(service, scenarios)
}

func assertExactSignedVerifiedCallback(
	t *testing.T,
	callback recordedCallback,
	sessionID uuid.UUID,
	webhookSecret string,
) {
	t.Helper()
	if callback.Method != httpMethod(http.MethodPost) || callback.Path != "/webhooks/verification" {
		t.Fatalf("callback target = %s %s, want POST /webhooks/verification", callback.Method, callback.Path)
	}
	if callback.Header.Get("Content-Type") != "application/json" {
		t.Errorf("callback Content-Type = %q, want application/json", callback.Header.Get("Content-Type"))
	}

	var event struct {
		EventID   uuid.UUID `json:"eventId"`
		SessionID uuid.UUID `json:"sessionId"`
		Verdict   string    `json:"verdict"`
	}
	decoder := json.NewDecoder(bytes.NewReader(callback.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("decode exact verified callback: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("verified callback has trailing JSON: %v", err)
	}
	if event.EventID == uuid.Nil || event.SessionID != sessionID || event.Verdict != "verified" {
		t.Fatalf("verified callback = %#v, want non-nil event ID for session %s", event, sessionID)
	}
	wantBody := fmt.Sprintf(
		`{"eventId":"%s","sessionId":"%s","verdict":"verified"}`,
		event.EventID,
		sessionID,
	)
	if string(callback.Body) != wantBody {
		t.Fatalf("verified callback body = %s, want exact %s", callback.Body, wantBody)
	}

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(callback.Body)
	wantSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if callback.Header.Get("x-signature") != wantSignature {
		t.Fatalf("callback signature does not authenticate the exact transmitted body")
	}
}

// recordedCallback preserves the callback request at the HTTP boundary. The body
// is copied before the handler returns so tests can compare the exact signed bytes.
type recordedCallback struct {
	Method httpMethod
	Path   string
	Header http.Header
	Body   []byte
}

// httpMethod is deliberately a distinct test type so fixtures cannot accidentally
// pass a method where application input is expected.
type httpMethod string

type callbackRecorder struct {
	server          *httptest.Server
	mu              sync.Mutex
	callbacks       []recordedCallback
	notify          chan struct{}
	responseRelease chan struct{}
	releaseOnce     sync.Once
}

func newCallbackRecorder(t *testing.T) *callbackRecorder {
	return newCallbackRecorderWithRelease(t, nil)
}

func newBlockingCallbackRecorder(t *testing.T) *callbackRecorder {
	return newCallbackRecorderWithRelease(t, make(chan struct{}))
}

func newCallbackRecorderWithRelease(t *testing.T, responseRelease chan struct{}) *callbackRecorder {
	t.Helper()

	recorder := &callbackRecorder{
		notify:          make(chan struct{}, 1),
		responseRelease: responseRelease,
	}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read callback", http.StatusBadRequest)
			return
		}

		callback := recordedCallback{
			Method: httpMethod(r.Method),
			Path:   r.URL.EscapedPath(),
			Header: r.Header.Clone(),
			Body:   append([]byte(nil), body...),
		}
		recorder.mu.Lock()
		recorder.callbacks = append(recorder.callbacks, callback)
		recorder.mu.Unlock()
		select {
		case recorder.notify <- struct{}{}:
		default:
		}
		if recorder.responseRelease != nil {
			select {
			case <-recorder.responseRelease:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(recorder.server.Close)
	t.Cleanup(recorder.Release)

	return recorder
}

func (r *callbackRecorder) URL() string { return r.server.URL }

func (r *callbackRecorder) Release() {
	if r.responseRelease != nil {
		r.releaseOnce.Do(func() { close(r.responseRelease) })
	}
}

func (r *callbackRecorder) Next(ctx context.Context) (recordedCallback, error) {
	for {
		r.mu.Lock()
		if len(r.callbacks) > 0 {
			callback := r.callbacks[0]
			r.callbacks = r.callbacks[1:]
			r.mu.Unlock()
			return callback, nil
		}
		r.mu.Unlock()

		select {
		case <-r.notify:
		case <-ctx.Done():
			return recordedCallback{}, ctx.Err()
		}
	}
}

func (r *callbackRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.callbacks)
}

// fixedScenarioStore is a test-only generic fixture. T remains unconstrained so
// this scaffold does not decide the production Scenario type or storage interface.
type fixedScenarioStore[T any] struct {
	mu        sync.Mutex
	defaultV  T
	bySession map[uuid.UUID]T
	lookups   []uuid.UUID
}

func newFixedScenarioStore[T any](defaultScenario T) *fixedScenarioStore[T] {
	return &fixedScenarioStore[T]{
		defaultV:  defaultScenario,
		bySession: make(map[uuid.UUID]T),
	}
}

func (s *fixedScenarioStore[T]) Set(sessionID uuid.UUID, scenario T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bySession[sessionID] = scenario
}

func (s *fixedScenarioStore[T]) Lookup(sessionID uuid.UUID) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups = append(s.lookups, sessionID)
	if scenario, ok := s.bySession[sessionID]; ok {
		return scenario
	}
	return s.defaultV
}

func (s *fixedScenarioStore[T]) LookupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lookups)
}

type providerHTTPHarness struct {
	server *httptest.Server
	client *http.Client
}

func newProviderHTTPHarness(t *testing.T, handler http.Handler) *providerHTTPHarness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &providerHTTPHarness{
		server: server,
		client: &http.Client{Timeout: fakeProviderHarnessTimeout},
	}
}

func (h *providerHTTPHarness) URL() string          { return h.server.URL }
func (h *providerHTTPHarness) Client() *http.Client { return h.client }

type scenarioRequestFixture struct {
	Name string
	Body string
}

var validScenarioRequestFixtures = []scenarioRequestFixture{
	{Name: "default verified", Body: `{"verdict":"verified","delayMs":0,"duplicateCallbacks":0}`},
	{Name: "rejected", Body: `{"verdict":"rejected","reason":"document_invalid","delayMs":50,"duplicateCallbacks":1}`},
}

var invalidScenarioRequestFixtures = []scenarioRequestFixture{
	{Name: "verified with reason", Body: `{"verdict":"verified","reason":"document_invalid","delayMs":0,"duplicateCallbacks":0}`},
	{Name: "rejected without reason", Body: `{"verdict":"rejected","delayMs":0,"duplicateCallbacks":0}`},
	{Name: "negative delay", Body: `{"verdict":"verified","delayMs":-1,"duplicateCallbacks":0}`},
	{Name: "negative duplicate count", Body: `{"verdict":"verified","delayMs":0,"duplicateCallbacks":-1}`},
}

func TestFakeProviderScaffoldFixtures(t *testing.T) {
	t.Run("callback recorder preserves exact request", func(t *testing.T) {
		recorder := newCallbackRecorder(t)
		wantBody := []byte("{\n  \"verdict\": \"verified\"\n}\n")
		request, err := http.NewRequest(http.MethodPost, recorder.URL()+"/webhooks/verification", bytes.NewReader(wantBody))
		if err != nil {
			t.Fatalf("new callback request: %v", err)
		}
		request.Header.Set("x-signature", "sha256=fixture")

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("send callback request: %v", err)
		}
		_ = response.Body.Close()

		ctx, cancel := context.WithTimeout(context.Background(), fakeProviderHarnessTimeout)
		defer cancel()
		got, err := recorder.Next(ctx)
		if err != nil {
			t.Fatalf("receive callback: %v", err)
		}
		if got.Method != httpMethod(http.MethodPost) || got.Path != "/webhooks/verification" ||
			string(got.Body) != string(wantBody) || got.Header.Get("x-signature") != "sha256=fixture" {
			t.Fatalf("recorded callback = %#v, want exact request", got)
		}
	})

	t.Run("fixed scenario store is deterministic", func(t *testing.T) {
		type scenario struct{ verdict string }
		defaultScenario := scenario{verdict: "verified"}
		rejectedScenario := scenario{verdict: "rejected"}
		sessionID := uuid.MustParse("fa31360d-910c-4ef3-a31e-9f5b05ed2661")
		store := newFixedScenarioStore(defaultScenario)

		if got := store.Lookup(sessionID); got != defaultScenario {
			t.Fatalf("default scenario = %#v, want %#v", got, defaultScenario)
		}
		store.Set(sessionID, rejectedScenario)
		if got := store.Lookup(sessionID); got != rejectedScenario {
			t.Fatalf("configured scenario = %#v, want %#v", got, rejectedScenario)
		}
		if got := store.LookupCount(); got != 2 {
			t.Fatalf("lookup count = %d, want 2", got)
		}
	})

	if len(validScenarioRequestFixtures) != 2 || len(invalidScenarioRequestFixtures) != 4 {
		t.Fatalf("scenario fixture counts changed without updating the Checkpoint 6 scaffold contract")
	}
}
