package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang/internal/application/fakeprovider"
)

func TestFakeProviderScenarioAcceptsVerifiedWithoutReason(t *testing.T) {
	sessionID := uuid.MustParse("696a37f8-dba2-4d62-9d75-2b3678928c83")
	store := &recordingFakeProviderScenarioStore{}
	handler := httpapi.NewFakeProviderScenarioHandler(nil, store)
	request := httptest.NewRequest(
		http.MethodPut,
		"/test/scenarios/"+sessionID.String(),
		strings.NewReader(`{"verdict":"verified","delayMs":0,"duplicateCallbacks":0}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		t.Fatalf("status = %d, want 2xx", response.Code)
	}
	if store.setCount != 1 {
		t.Fatalf("scenario store calls = %d, want 1", store.setCount)
	}
	if store.sessionID != sessionID {
		t.Fatalf("stored session ID = %s, want %s", store.sessionID, sessionID)
	}
	want := fakeprovider.Scenario{Verdict: fakeprovider.Verified}
	if store.scenario != want {
		t.Fatalf("stored scenario = %#v, want %#v", store.scenario, want)
	}
}

func TestFakeProviderLiveHealthContract(t *testing.T) {
	handler := httpapi.NewFakeProviderScenarioHandler(nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestFakeProviderScenarioAcceptsEveryRejectedReason(t *testing.T) {
	sessionID := uuid.MustParse("13b718e7-8689-42ca-a53d-a31e748ac465")
	reasons := []fakeprovider.RejectionReason{
		fakeprovider.DocumentInvalid,
		fakeprovider.BiometricMismatch,
		fakeprovider.IdentityNotVerified,
		fakeprovider.SuspectedFraud,
	}

	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			store := &recordingFakeProviderScenarioStore{}
			handler := httpapi.NewFakeProviderScenarioHandler(nil, store)
			body := fmt.Sprintf(
				`{"verdict":"rejected","reason":%q,"delayMs":0,"duplicateCallbacks":0}`,
				reason,
			)
			request := httptest.NewRequest(
				http.MethodPut,
				"/test/scenarios/"+sessionID.String(),
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			want := fakeprovider.Scenario{Verdict: fakeprovider.Rejected, Reason: reason}
			if store.setCount != 1 || store.sessionID != sessionID || store.scenario != want {
				t.Fatalf("stored scenario = %#v for session %s after %d calls, want %#v", store.scenario, store.sessionID, store.setCount, want)
			}
		})
	}
}

func TestFakeProviderScenarioRejectsInvalidInvariant(t *testing.T) {
	sessionID := uuid.MustParse("c181cfc7-fdaa-4221-b5e5-aceca7fa197d")
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown verdict", body: `{"verdict":"pending","delayMs":0,"duplicateCallbacks":0}`},
		{name: "verified with reason", body: `{"verdict":"verified","reason":"document_invalid","delayMs":0,"duplicateCallbacks":0}`},
		{name: "rejected without reason", body: `{"verdict":"rejected","delayMs":0,"duplicateCallbacks":0}`},
		{name: "rejected with unknown reason", body: `{"verdict":"rejected","reason":"provider_unavailable","delayMs":0,"duplicateCallbacks":0}`},
		{name: "negative delay", body: `{"verdict":"verified","delayMs":-1,"duplicateCallbacks":0}`},
		{name: "negative duplicate count", body: `{"verdict":"verified","delayMs":0,"duplicateCallbacks":-1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &recordingFakeProviderScenarioStore{}
			handler := httpapi.NewFakeProviderScenarioHandler(nil, store)
			request := httptest.NewRequest(
				http.MethodPut,
				"/test/scenarios/"+sessionID.String(),
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if store.setCount != 0 {
				t.Fatalf("scenario store calls = %d, want 0", store.setCount)
			}
		})
	}
}

func TestFakeProviderScenarioBoundsDuplicateCallbacksAtTen(t *testing.T) {
	sessionID := uuid.MustParse("77d5d83c-cd15-41bb-9a04-56d95e034762")
	tests := []struct {
		name          string
		count         int
		wantStatus    int
		wantStoreCall int
	}{
		{name: "maximum accepted", count: 10, wantStatus: http.StatusOK, wantStoreCall: 1},
		{name: "above maximum rejected", count: 11, wantStatus: http.StatusBadRequest, wantStoreCall: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &recordingFakeProviderScenarioStore{}
			handler := httpapi.NewFakeProviderScenarioHandler(nil, store)
			body := fmt.Sprintf(
				`{"verdict":"verified","delayMs":0,"duplicateCallbacks":%d}`,
				tt.count,
			)
			request := httptest.NewRequest(
				http.MethodPut,
				"/test/scenarios/"+sessionID.String(),
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if store.setCount != tt.wantStoreCall {
				t.Fatalf("scenario store calls = %d, want %d", store.setCount, tt.wantStoreCall)
			}
		})
	}
}

func TestFakeProviderRejectsMalformedRequestsBeforeMutation(t *testing.T) {
	sessionID := uuid.MustParse("cb93ab6d-8094-47a1-9e9c-6a3beaa7623f")
	tests := []struct {
		name       string
		path       string
		body       string
		submission bool
	}{
		{name: "empty submission", path: "/", body: "", submission: true},
		{name: "malformed submission", path: "/", body: `{`, submission: true},
		{name: "unknown submission field", path: "/", body: `{"unknown":true}`, submission: true},
		{name: "trailing submission value", path: "/", body: `{}` + `{}`, submission: true},
		{name: "empty scenario", path: "/test/scenarios/" + sessionID.String(), body: ""},
		{name: "malformed scenario", path: "/test/scenarios/" + sessionID.String(), body: `{`},
		{name: "unknown scenario field", path: "/test/scenarios/" + sessionID.String(), body: `{"unknown":true}`},
		{name: "trailing scenario value", path: "/test/scenarios/" + sessionID.String(), body: `{}` + `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingFakeSubmissionService{}
			store := &recordingFakeProviderScenarioStore{}
			handler := httpapi.NewFakeProviderScenarioHandler(service, store)
			method := http.MethodPut
			if tt.submission {
				method = http.MethodPost
			}
			request := httptest.NewRequest(method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", sessionID.String())
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertFakeProviderError(t, response, http.StatusBadRequest, "VALIDATION_ERROR")
			if service.calls != 0 || store.setCount != 0 {
				t.Fatalf("malformed request mutated dependencies: service=%d store=%d", service.calls, store.setCount)
			}
		})
	}
}

func TestFakeProviderRejectsOversizedRequestsBeforeMutation(t *testing.T) {
	sessionID := uuid.MustParse("531882f7-dcb0-4a8e-8106-22a708a05e1c")
	oversized := strings.Repeat("x", (1<<20)+1)
	tests := []struct {
		name       string
		path       string
		body       string
		submission bool
	}{
		{name: "submission", path: "/", body: `{"sessionId":"` + oversized + `"}`, submission: true},
		{name: "scenario", path: "/test/scenarios/" + sessionID.String(), body: `{"verdict":"` + oversized + `"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingFakeSubmissionService{}
			store := &recordingFakeProviderScenarioStore{}
			handler := httpapi.NewFakeProviderScenarioHandler(service, store)
			method := http.MethodPut
			if tt.submission {
				method = http.MethodPost
			}
			request := httptest.NewRequest(method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", sessionID.String())
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertFakeProviderError(t, response, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE")
			if service.calls != 0 || store.setCount != 0 {
				t.Fatalf("oversized request mutated dependencies: service=%d store=%d", service.calls, store.setCount)
			}
		})
	}
}

func TestFakeProviderSubmissionRejectsInvalidIdempotencyKeyBeforeService(t *testing.T) {
	sessionID := uuid.MustParse("a973ab7a-ab04-4a99-b634-174703982628")
	validBody := fmt.Sprintf(
		`{"sessionId":"%s","callbackUrl":"http://callback.example.test","personalDetails":{},"identityDocument":{},"biometricCapture":{}}`,
		sessionID,
	)
	tests := []struct {
		name string
		key  string
	}{
		{name: "missing"},
		{name: "malformed", key: "not-a-uuid"},
		{name: "different from session", key: "006470d9-1e71-40b3-91c9-eabc0b26d842"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &recordingFakeSubmissionService{}
			handler := httpapi.NewFakeProviderScenarioHandler(service, nil)
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(validBody))
			request.Header.Set("Content-Type", "application/json")
			if tt.key != "" {
				request.Header.Set("Idempotency-Key", tt.key)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertFakeProviderError(t, response, http.StatusBadRequest, "VALIDATION_ERROR")
			if service.calls != 0 {
				t.Fatalf("service calls = %d, want 0", service.calls)
			}
		})
	}
}

func assertFakeProviderError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var got httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if got.Code != wantCode {
		t.Fatalf("error code = %q, want %q", got.Code, wantCode)
	}
}

type recordingFakeProviderScenarioStore struct {
	setCount  int
	sessionID uuid.UUID
	scenario  fakeprovider.Scenario
}

type recordingFakeSubmissionService struct {
	calls int
}

func (s *recordingFakeSubmissionService) Accept(context.Context, uuid.UUID, fakeprovider.ProviderSubmissionRequest) (bool, error) {
	s.calls++
	return false, nil
}

func (s *recordingFakeProviderScenarioStore) Set(sessionID uuid.UUID, scenario fakeprovider.Scenario) {
	s.setCount++
	s.sessionID = sessionID
	s.scenario = scenario
}
