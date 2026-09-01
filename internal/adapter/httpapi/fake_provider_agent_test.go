package httpapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
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

type recordingFakeProviderScenarioStore struct {
	setCount  int
	sessionID uuid.UUID
	scenario  fakeprovider.Scenario
}

func (s *recordingFakeProviderScenarioStore) Set(sessionID uuid.UUID, scenario fakeprovider.Scenario) {
	s.setCount++
	s.sessionID = sessionID
	s.scenario = scenario
}
