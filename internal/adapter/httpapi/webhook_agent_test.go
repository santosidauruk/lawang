package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang/internal/application/providerverdict"
)

func TestWebhookRejectsMissingSignatureWithSafeUnauthorizedError(t *testing.T) {
	service := &agentVerifiedBodyService{}
	request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", nil)
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	assertWebhookError(t, response, http.StatusUnauthorized, "INVALID_SIGNATURE", "invalid webhook signature")
	if service.calls != 0 {
		t.Fatalf("HandleVerifiedBody() calls = %d, want 0", service.calls)
	}
}

func TestWebhookRejectsMalformedAndInvalidSignaturesBeforeService(t *testing.T) {
	validMAC, err := setHmacVerifiedBody(checkpoint2WebhookSecret, checkpoint2ExactWebhookBody)
	if err != nil {
		t.Fatalf("sign exact webhook body: %v", err)
	}
	tests := []struct {
		name   string
		header string
	}{
		{name: "wrong scheme", header: "sha1=" + validMAC},
		{name: "empty hex", header: "sha256="},
		{name: "malformed hex", header: "sha256=not-hex"},
		{name: "odd-length hex", header: "sha256=abc"},
		{name: "invalid MAC", header: "sha256=" + strings.Repeat("0", sha256HexLength)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &agentVerifiedBodyService{}
			request := httptest.NewRequest(
				http.MethodPost,
				"/webhooks/verification",
				strings.NewReader(string(checkpoint2ExactWebhookBody)),
			)
			request.Header.Set("x-signature", test.header)
			response := httptest.NewRecorder()

			newAgentWebhookHandler(service).ServeHTTP(response, request)

			assertWebhookError(t, response, http.StatusUnauthorized, "INVALID_SIGNATURE", "invalid webhook signature")
			if service.calls != 0 {
				t.Fatalf("HandleVerifiedBody() calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestWebhookAcceptsUppercaseHexSignature(t *testing.T) {
	validMAC, err := setHmacVerifiedBody(checkpoint2WebhookSecret, checkpoint2ExactWebhookBody)
	if err != nil {
		t.Fatalf("sign exact webhook body: %v", err)
	}
	service := &agentVerifiedBodyService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		strings.NewReader(string(checkpoint2ExactWebhookBody)),
	)
	request.Header.Set("x-signature", "sha256="+strings.ToUpper(validMAC))
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.calls != 1 {
		t.Fatalf("HandleVerifiedBody() calls = %d, want 1", service.calls)
	}
	if len(service.bodies) != 1 || !bytes.Equal(service.bodies[0], checkpoint2ExactWebhookBody) {
		t.Fatalf("forwarded bodies = %q, want exact body %q", service.bodies, checkpoint2ExactWebhookBody)
	}
}

func TestWebhookRejectsOversizedBodyBeforeSignatureVerification(t *testing.T) {
	service := &agentVerifiedBodyService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		bytes.NewReader(bytes.Repeat([]byte("x"), (1<<20)+1)),
	)
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	assertWebhookError(
		t,
		response,
		http.StatusRequestEntityTooLarge,
		"PAYLOAD_TOO_LARGE",
		"request body exceeds 1 MiB limit",
	)
	if service.calls != 0 {
		t.Fatalf("HandleVerifiedBody() calls = %d, want 0", service.calls)
	}
}

func TestWebhookBoundsRequestBodyReadFailure(t *testing.T) {
	const sensitiveFailure = "reader exposed secret and raw webhook body"
	service := &agentVerifiedBodyService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		&failingWebhookBody{err: errors.New(sensitiveFailure)},
	)
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	assertWebhookError(t, response, http.StatusInternalServerError, "INTERNAL", "internal server error")
	if strings.Contains(response.Body.String(), sensitiveFailure) {
		t.Fatalf("error response exposed request reader failure: %s", response.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("HandleVerifiedBody() calls = %d, want 0", service.calls)
	}
}

func TestWebhookBoundsVerifiedBodyServiceFailure(t *testing.T) {
	const sensitiveFailure = "service exposed checkpoint-2-fixed-webhook-secret and raw callback"
	validMAC, err := setHmacVerifiedBody(checkpoint2WebhookSecret, checkpoint2ExactWebhookBody)
	if err != nil {
		t.Fatalf("sign exact webhook body: %v", err)
	}
	service := &agentVerifiedBodyService{err: errors.New(sensitiveFailure)}
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		bytes.NewReader(checkpoint2ExactWebhookBody),
	)
	request.Header.Set("x-signature", "sha256="+validMAC)
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	assertWebhookError(t, response, http.StatusInternalServerError, "INTERNAL", "internal server error")
	if strings.Contains(response.Body.String(), sensitiveFailure) ||
		strings.Contains(response.Body.String(), checkpoint2WebhookSecret) {
		t.Fatalf("error response exposed service failure or webhook secret: %s", response.Body.String())
	}
	if service.calls != 1 || len(service.bodies) != 1 ||
		!bytes.Equal(service.bodies[0], checkpoint2ExactWebhookBody) {
		t.Fatalf("service received calls=%d bodies=%q, want one exact body", service.calls, service.bodies)
	}
}

func TestWebhookForwardsSignedMalformedJSONWithoutDecoding(t *testing.T) {
	malformedJSON := []byte(`{"eventId":`)
	validMAC, err := setHmacVerifiedBody(checkpoint2WebhookSecret, malformedJSON)
	if err != nil {
		t.Fatalf("sign malformed JSON bytes: %v", err)
	}
	service := &agentVerifiedBodyService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/webhooks/verification",
		bytes.NewReader(malformedJSON),
	)
	request.Header.Set("x-signature", "sha256="+validMAC)
	response := httptest.NewRecorder()

	newAgentWebhookHandler(service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.calls != 1 || len(service.bodies) != 1 || !bytes.Equal(service.bodies[0], malformedJSON) {
		t.Fatalf("service received calls=%d bodies=%q, want one exact malformed body", service.calls, service.bodies)
	}
}

func TestWebhookRequestLoggingExcludesBodySignatureAndSecret(t *testing.T) {
	const (
		webhookSecret = "do-not-log-webhook-secret"
		bodyMarker    = "do-not-log-webhook-body"
	)
	rawBody := []byte(`{"marker":"` + bodyMarker + `"}`)
	validMAC, err := setHmacVerifiedBody(webhookSecret, rawBody)
	if err != nil {
		t.Fatalf("sign webhook body: %v", err)
	}
	service := &agentVerifiedBodyService{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := httpapi.WithRequestLogging(
		httpapi.NewHandler(nil, nil, nil, nil, &httpapi.VerifiedBody{
			Service: service, ProviderWebhookSecret: webhookSecret,
		}, nil),
		logger,
	)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(rawBody))
	request.Header.Set("x-signature", "sha256="+validMAC)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode request log: %v; output=%s", err, logs.String())
	}
	for key, want := range map[string]any{
		"method":     http.MethodPost,
		"route":      "/webhooks/verification",
		"status":     float64(http.StatusOK),
		"error_code": "",
	} {
		if got := entry[key]; got != want {
			t.Errorf("log[%q] = %#v, want %#v", key, got, want)
		}
	}
	for _, sensitive := range []string{webhookSecret, bodyMarker, validMAC, "x-signature"} {
		if strings.Contains(logs.String(), sensitive) {
			t.Errorf("request log exposed %q: %s", sensitive, logs.String())
		}
	}
}

const sha256HexLength = 64

type agentVerifiedBodyService struct {
	calls  int
	bodies [][]byte
	err    error
}

type failingWebhookBody struct {
	err error
}

func (body *failingWebhookBody) Read(_ []byte) (int, error) { return 0, body.err }
func (body *failingWebhookBody) Close() error               { return nil }

func (service *agentVerifiedBodyService) HandleVerifiedBody(_ context.Context, body []byte) (providerverdict.ApplyInput, error) {
	service.calls++
	service.bodies = append(service.bodies, append([]byte(nil), body...))
	return providerverdict.ApplyInput{}, service.err
}

func (service *agentVerifiedBodyService) Apply(ctx context.Context, input providerverdict.ApplyInput) error {
	return nil
}

func newAgentWebhookHandler(service httpapi.VerifiedBodyService) http.Handler {
	return httpapi.NewHandler(nil, nil, nil, nil, &httpapi.VerifiedBody{
		Service:               service,
		ProviderWebhookSecret: checkpoint2WebhookSecret,
	}, nil)
}

func assertWebhookError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantMessage string,
) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var body httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != wantCode || body.Message != wantMessage || body.Details != nil {
		t.Errorf("error = %#v, want code=%q message=%q without details", body, wantCode, wantMessage)
	}
}
