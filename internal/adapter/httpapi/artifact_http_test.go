package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
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

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

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

type artifactUploadIntentServiceStub struct {
	created   artifact.CreatedUploadIntent
	err       error
	calls     int
	sessionID uuid.UUID
	token     string
	kind      string
}

func (service *artifactUploadIntentServiceStub) Create(
	_ context.Context,
	sessionID uuid.UUID,
	token string,
	kind string,
) (artifact.CreatedUploadIntent, error) {
	service.calls++
	service.sessionID = sessionID
	service.token = token
	service.kind = kind
	return service.created, service.err
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

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

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

func TestConfirmIdentityDocumentRequiresUploadIntentID(t *testing.T) {
	sessionID, _, _, service := newArtifactConfirmHTTPFixture()
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		bytes.NewBufferString(`{}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", apiError.Code)
	}
	if apiError.Message != "uploadIntentId is required" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"uploadIntentId is required",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentRejectsInvalidUploadIntentID(t *testing.T) {
	sessionID, _, _, service := newArtifactConfirmHTTPFixture()
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		bytes.NewBufferString(`{"uploadIntentId":"not-a-uuid"}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", apiError.Code)
	}
	if apiError.Message != "uploadIntentId must be a UUID" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"uploadIntentId must be a UUID",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentRejectsMalformedJSON(t *testing.T) {
	sessionID, _, _, service := newArtifactConfirmHTTPFixture()
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		bytes.NewBufferString(`{"uploadIntentId":]}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", apiError.Code)
	}
	if apiError.Message != "invalid artifact confirmation request" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"invalid artifact confirmation request",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentRejectsEmptyBody(t *testing.T) {
	sessionID, _, _, service := newArtifactConfirmHTTPFixture()
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		http.NoBody,
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", apiError.Code)
	}
	if apiError.Message != "invalid artifact confirmation request" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"invalid artifact confirmation request",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentRejectsSecondJSONValue(t *testing.T) {
	sessionID, uploadIntentID, _, service := newArtifactConfirmHTTPFixture()
	body := fmt.Sprintf(`{"uploadIntentId":%q}{}`, uploadIntentID.String())
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusBadRequest,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", apiError.Code)
	}
	if apiError.Message != "invalid artifact confirmation request" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"invalid artifact confirmation request",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentRejectsBodyAboveOneMiB(t *testing.T) {
	sessionID, _, _, service := newArtifactConfirmHTTPFixture()
	body := `{"uploadIntentId":"` + strings.Repeat("a", (1<<20)+1) + `"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusRequestEntityTooLarge,
			response.Body.String(),
		)
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "PAYLOAD_TOO_LARGE" {
		t.Errorf("error code = %q, want PAYLOAD_TOO_LARGE", apiError.Code)
	}
	if apiError.Message != "request body exceeds 1 MiB limit" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"request body exceeds 1 MiB limit",
		)
	}
	if service.calls != 0 {
		t.Errorf("Confirm() calls = %d, want 0", service.calls)
	}
}

func TestConfirmIdentityDocumentHTTPBoundaryRejections(t *testing.T) {
	sessionID, uploadIntentID, _, _ := newArtifactConfirmHTTPFixture()
	validBody := fmt.Sprintf(`{"uploadIntentId":%q}`, uploadIntentID.String())
	tests := []struct {
		name          string
		method        string
		pathID        string
		authorization string
		wantStatus    int
		wantCode      string
		wantMessage   string
		wantAllow     string
	}{
		{
			name: "missing authorization", method: http.MethodPost, pathID: sessionID.String(),
			wantStatus: http.StatusUnauthorized, wantCode: "MISSING_AUTHORIZATION",
			wantMessage: "Auth header required",
		},
		{
			name: "malformed authorization", method: http.MethodPost, pathID: sessionID.String(),
			authorization: "Bearer one two",
			wantStatus:    http.StatusUnauthorized, wantCode: "MALFORMED_AUTHORIZATION",
			wantMessage: "expected Bearer <token>",
		},
		{
			name: "invalid session id", method: http.MethodPost, pathID: "not-a-uuid",
			authorization: "Bearer opaque-token",
			wantStatus:    http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
			wantMessage: "id must be a UUID",
		},
		{
			name: "wrong method", method: http.MethodGet, pathID: sessionID.String(),
			authorization: "Bearer opaque-token",
			wantStatus:    http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED",
			wantMessage: "method not allowed", wantAllow: http.MethodPost,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, service := newArtifactConfirmHTTPFixture()
			request := httptest.NewRequest(
				test.method,
				"/verification-sessions/"+test.pathID+"/artifacts/confirm",
				strings.NewReader(validBody),
			)
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want = %d; body = %s",
					response.Code,
					test.wantStatus,
					response.Body.String(),
				)
			}
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Errorf("Allow = %q, want %q", got, test.wantAllow)
			}
			var apiError httpapi.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if apiError.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", apiError.Code, test.wantCode)
			}
			if apiError.Message != test.wantMessage {
				t.Errorf("error message = %q, want %q", apiError.Message, test.wantMessage)
			}
			if service.calls != 0 {
				t.Errorf("Confirm() calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestConfirmIdentityDocumentMapsIdentityMismatch(t *testing.T) {
	sessionID, uploadIntentID, _, service := newArtifactConfirmHTTPFixture()
	service.err = &artifact.Error{
		Code:   artifact.CodeLocalValidationFailed,
		Reason: artifact.ReasonIdentityNumberMismatch,
	}
	body := fmt.Sprintf(`{"uploadIntentId":%q}`, uploadIntentID.String())
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusUnprocessableEntity,
			response.Body.String(),
		)
	}
	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != "LOCAL_VALIDATION_FAILED" {
		t.Errorf("error code = %q, want LOCAL_VALIDATION_FAILED", apiError.Code)
	}
	if apiError.Message != "The uploaded identity document did not match the submitted details" {
		t.Errorf(
			"error message = %q, want %q",
			apiError.Message,
			"The uploaded identity document did not match the submitted details",
		)
	}
	if len(apiError.Details) != 1 ||
		apiError.Details["reason"] != string(artifact.ReasonIdentityNumberMismatch) {
		t.Errorf(
			"error details = %#v, want only reason %q",
			apiError.Details,
			artifact.ReasonIdentityNumberMismatch,
		)
	}
	if service.calls != 1 {
		t.Errorf("Confirm() calls = %d, want 1", service.calls)
	}
	if strings.Contains(response.Body.String(), "identityNumber") {
		t.Errorf("response leaks identity-number field: %s", response.Body.String())
	}
}

func TestConfirmIdentityDocumentMapsUploadIntentNotFound(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeUploadIntentNotFound},
		http.StatusNotFound,
		"UPLOAD_INTENT_NOT_FOUND",
		"upload intent not found",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsUploadIntentExpired(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeUploadIntentExpired},
		http.StatusConflict,
		"UPLOAD_INTENT_EXPIRED",
		"upload intent expired",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsUploadIntentSuperseded(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeUploadIntentSuperseded},
		http.StatusConflict,
		"UPLOAD_INTENT_SUPERSEDED",
		"upload intent was superseded",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsInvalidUploadIntentKind(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeInvalidUploadIntentKind},
		http.StatusConflict,
		"INVALID_UPLOAD_INTENT_KIND",
		"upload intent kind is not valid for artifact confirmation",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsInvalidObjectMetadata(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{
			Code:   artifact.CodeInvalidObjectMetadata,
			Reason: artifact.ReasonObjectTooLarge,
		},
		http.StatusUnprocessableEntity,
		"INVALID_OBJECT_METADATA",
		"uploaded object metadata is invalid",
		map[string]any{"reason": string(artifact.ReasonObjectTooLarge)},
	)
}

func TestConfirmIdentityDocumentMapsConfirmationStale(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeConfirmationStale},
		http.StatusConflict,
		"CONFIRMATION_STALE",
		"artifact confirmation state changed; retry the request",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsObjectStorageFailure(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeObjectStorageFailed},
		http.StatusServiceUnavailable,
		"OBJECT_STORAGE_FAILED",
		"object storage is temporarily unavailable",
		nil,
	)
}

func TestConfirmIdentityDocumentMapsDocumentExtractionFailure(t *testing.T) {
	assertArtifactConfirmError(
		t,
		&artifact.Error{Code: artifact.CodeDocumentExtractionFailed},
		http.StatusInternalServerError,
		"DOCUMENT_EXTRACTION_FAILED",
		"identity document processing failed",
		nil,
	)
}

func TestCreateIdentityDocumentUploadIntentHTTPContract(t *testing.T) {
	sessionID := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	uploadIntentID := uuid.MustParse("21b15c5e-d8f3-4b79-ac47-d4f5fd55ac4c")
	service := &artifactUploadIntentServiceStub{
		created: artifact.CreatedUploadIntent{
			ID:        uploadIntentID,
			UploadURL: "https://public-object-host/upload",
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/upload-url",
		strings.NewReader(`{"kind":"identity_document"}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, nil, service, nil).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			http.StatusCreated,
			response.Body.String(),
		)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode response fields: %v", err)
	}
	if len(fields) != 2 || fields["uploadIntentId"] == nil || fields["uploadUrl"] == nil {
		t.Errorf(
			"response fields = %v, want only uploadIntentId and uploadUrl",
			fields,
		)
	}
	var responseBody struct {
		UploadIntentID uuid.UUID `json:"uploadIntentId"`
		UploadURL      string    `json:"uploadUrl"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if responseBody.UploadIntentID != uploadIntentID {
		t.Errorf(
			"uploadIntentId = %s, want %s",
			responseBody.UploadIntentID,
			uploadIntentID,
		)
	}
	if responseBody.UploadURL != service.created.UploadURL {
		t.Errorf("uploadUrl = %q, want %q", responseBody.UploadURL, service.created.UploadURL)
	}
	if service.calls != 1 {
		t.Errorf("Create() calls = %d, want 1", service.calls)
	}
	if service.sessionID != sessionID {
		t.Errorf("sessionID = %s, want %s", service.sessionID, sessionID)
	}
	if service.token != "opaque-token" {
		t.Errorf("token = %q, want opaque-token", service.token)
	}
	if service.kind != "identity_document" {
		t.Errorf("kind = %q, want identity_document", service.kind)
	}
}

func assertArtifactConfirmError(
	t *testing.T,
	serviceError error,
	wantStatus int,
	wantCode string,
	wantMessage string,
	wantDetails map[string]any,
) {
	t.Helper()
	sessionID, uploadIntentID, _, service := newArtifactConfirmHTTPFixture()
	service.err = serviceError
	body := fmt.Sprintf(`{"uploadIntentId":%q}`, uploadIntentID.String())
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil, service, nil, nil).ServeHTTP(response, request)

	if response.Code != wantStatus {
		t.Fatalf(
			"status = %d, want = %d; body = %s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Code != wantCode {
		t.Errorf("error code = %q, want %q", apiError.Code, wantCode)
	}
	if apiError.Message != wantMessage {
		t.Errorf("error message = %q, want %q", apiError.Message, wantMessage)
	}
	if fmt.Sprint(apiError.Details) != fmt.Sprint(wantDetails) {
		t.Errorf("error details = %#v, want %#v", apiError.Details, wantDetails)
	}
	if service.calls != 1 {
		t.Errorf("Confirm() calls = %d, want 1", service.calls)
	}
}
