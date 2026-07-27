package httpapi_test

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
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

// artifactConfirmServiceStub is the HTTP-boundary fake for Checkpoint 4.
//
// User checkpoint:
//  1. Add TestConfirmIdentityDocumentHTTPContract above this type.
//  2. Drive the request through httpapi.NewHandler.
//  3. Assert the exact HTTP response and the captured Confirm arguments.
//
// Keep object storage, extraction, and transaction behavior out of this fake. Those
// are application concerns already covered by artifact.Service tests.
func TestConfirmIdentityDocumentHTTPContract(t *testing.T) {
	sessionID, uploadIntentID, expiresAt, service := newArtifactConfirmHTTPFixture()
	token := "opaque-token"
	payload := map[string]string{"uploadIntentId": uploadIntentID.String()}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload %v", payload)
	}
	request := httptest.NewRequest(http.MethodPost, "/verification-sessions/"+sessionID.String()+"/artifacts/confirm", bytes.NewReader(body))
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want = %d", response.Code, http.StatusOK)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("response header = %s, want application/json", got)
	}

	rawResponse := response.Body.Bytes()

	var responseBody struct {
		ID        uuid.UUID      `json:"id"`
		Status    session.Status `json:"status"`
		ExpiresAt time.Time      `json:"expiresAt"`
	}

	if err := json.Unmarshal(rawResponse, &responseBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	var responseFields map[string]json.RawMessage
	if err := json.Unmarshal(rawResponse, &responseFields); err != nil {
		t.Fatalf("decode response fields: %v", err)
	}

	expectedFields := map[string]struct{}{
		"id":        {},
		"status":    {},
		"expiresAt": {},
	}

	if len(responseFields) != len(expectedFields) {
		t.Errorf("got length of response: %d, want %d", len(responseFields), len(expectedFields))
	}

	for field := range expectedFields {
		if _, exists := responseFields[field]; !exists {
			t.Errorf("response is missing field %q", field)
		}
	}

	for field := range responseFields {
		if _, expected := expectedFields[field]; !expected {
			t.Errorf("response contains unexpected field %q", field)
		}
	}

	if responseBody.ID != sessionID {
		t.Errorf("response ID got = %v, want = %v", responseBody.ID, sessionID)
	}
	if responseBody.Status != session.StatusIdentityDocumentUploaded {
		t.Errorf("response status got = %v, want = %v", responseBody.Status, session.StatusIdentityDocumentUploaded)
	}

	if !responseBody.ExpiresAt.Equal(expiresAt) {
		t.Errorf("response expiresAt got = %v want = %v", responseBody.ExpiresAt.String(), expiresAt.String())
	}

	if service.sessionID != sessionID {
		t.Errorf("sessionID got = %s, want %s", service.sessionID, sessionID)
	}

	if service.token != token {
		t.Errorf("token got = %s, want %s", service.token, token)
	}
	if service.uploadIntentID != uploadIntentID {
		t.Errorf("uploadIntentID got = %s, want %s", service.uploadIntentID, uploadIntentID)
	}

	if service.calls != 1 {
		t.Errorf("service is not called 1 time")
	}
}

type artifactConfirmServiceStub struct {
	summary        session.Summary
	err            error
	calls          int
	sessionID      uuid.UUID
	token          string
	uploadIntentID uuid.UUID
}

func (service *artifactConfirmServiceStub) Confirm(
	_ context.Context,
	sessionID uuid.UUID,
	token string,
	uploadIntentID uuid.UUID,
) (session.Summary, error) {
	service.calls++
	service.sessionID = sessionID
	service.token = token
	service.uploadIntentID = uploadIntentID

	return service.summary, service.err
}

// Fixed inputs for the first user-authored tracer. Keeping these in one helper
// makes the exact response and service-call assertions easier to read.
func newArtifactConfirmHTTPFixture() (
	sessionID uuid.UUID,
	uploadIntentID uuid.UUID,
	expiresAt time.Time,
	service *artifactConfirmServiceStub,
) {
	sessionID = uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	uploadIntentID = uuid.MustParse("21b15c5e-d8f3-4b79-ac47-d4f5fd55ac4c")
	expiresAt = time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	service = &artifactConfirmServiceStub{
		summary: session.Summary{
			ID:        sessionID,
			Status:    session.StatusIdentityDocumentUploaded,
			ExpiresAt: expiresAt,
		},
	}

	return sessionID, uploadIntentID, expiresAt, service
}

func TestConfirmIdentityDocumentRejectsUnknownField(t *testing.T) {
	sessionID, uploadIntentID, _, service := newArtifactConfirmHTTPFixture()
	token := "opaque-token"
	payload := map[string]string{"uploadIntentId": uploadIntentID.String(), "extra": "should be rejected"}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload %v", payload)
	}
	request := httptest.NewRequest(http.MethodPost, "/verification-sessions/"+sessionID.String()+"/artifacts/confirm", bytes.NewReader(body))
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want = %d; body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf(
			"error code = %q, want VALIDATION_ERROR", apiError.Code,
		)
	}

	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}
