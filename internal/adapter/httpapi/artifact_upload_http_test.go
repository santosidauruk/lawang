package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestCreateIdentityDocumentUploadIntentRejectsUnknownField(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{"kind":"identity_document","extra":"rejected"}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusBadRequest,
		"VALIDATION_ERROR",
		"invalid artifact upload URL request",
	)
	if service.calls != 0 {
		t.Errorf("Create() calls = %d, want 0", service.calls)
	}
}

func TestCreateIdentityDocumentUploadIntentRejectsSecondJSONValue(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{"kind":"identity_document"}{}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusBadRequest,
		"VALIDATION_ERROR",
		"invalid artifact upload URL request",
	)
	if service.calls != 0 {
		t.Errorf("Create() calls = %d, want 0", service.calls)
	}
}

func TestCreateIdentityDocumentUploadIntentRejectsBodyAboveOneMiB(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{"kind":"`+strings.Repeat("a", (1<<20)+1)+`"}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusRequestEntityTooLarge,
		"PAYLOAD_TOO_LARGE",
		"request body exceeds 1 MiB limit",
	)
	if service.calls != 0 {
		t.Errorf("Create() calls = %d, want 0", service.calls)
	}
}

func TestCreateIdentityDocumentUploadIntentRejectsMissingKind(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusBadRequest,
		"INVALID_UPLOAD_INTENT_KIND",
		"only identity_document uploads are supported",
	)
	if service.calls != 0 {
		t.Errorf("Create() calls = %d, want 0", service.calls)
	}
}

func TestCreateIdentityDocumentUploadIntentRejectsUnsupportedKind(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	service.err = &artifact.Error{Code: artifact.CodeInvalidUploadIntentKind}
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{"kind":"biometric_capture"}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusBadRequest,
		"INVALID_UPLOAD_INTENT_KIND",
		"only identity_document uploads are supported",
	)
	if service.calls != 1 {
		t.Errorf("Create() calls = %d, want 1", service.calls)
	}
}

func TestCreateIdentityDocumentUploadIntentMapsStaleState(t *testing.T) {
	service := newArtifactUploadIntentHTTPFixture()
	service.err = &artifact.Error{Code: artifact.CodeUploadIntentStale}
	response := performArtifactUploadIntentRequest(
		t,
		service,
		http.MethodPost,
		"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
		"Bearer opaque-token",
		`{"kind":"identity_document"}`,
	)

	assertArtifactUploadIntentError(
		t,
		response,
		http.StatusConflict,
		"UPLOAD_INTENT_STALE",
		"upload intent state changed; retry the request",
	)
}

func TestCreateIdentityDocumentUploadIntentRejectsInvalidJSONFraming(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"kind":]}`},
		{name: "empty"},
		{name: "wrong kind type", body: `{"kind":123}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newArtifactUploadIntentHTTPFixture()
			response := performArtifactUploadIntentRequest(
				t,
				service,
				http.MethodPost,
				"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
				"Bearer opaque-token",
				test.body,
			)
			assertArtifactUploadIntentError(
				t,
				response,
				http.StatusBadRequest,
				"VALIDATION_ERROR",
				"invalid artifact upload URL request",
			)
			if service.calls != 0 {
				t.Errorf("Create() calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestCreateIdentityDocumentUploadIntentHTTPBoundaryRejections(t *testing.T) {
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
			name: "missing authorization", method: http.MethodPost,
			pathID:     "4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
			wantStatus: http.StatusUnauthorized, wantCode: "MISSING_AUTHORIZATION",
			wantMessage: "Auth header required",
		},
		{
			name: "malformed authorization", method: http.MethodPost,
			pathID:        "4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
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
			name: "wrong method", method: http.MethodGet,
			pathID:        "4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
			authorization: "Bearer opaque-token",
			wantStatus:    http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED",
			wantMessage: "method not allowed", wantAllow: http.MethodPost,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newArtifactUploadIntentHTTPFixture()
			response := performArtifactUploadIntentRequest(
				t,
				service,
				test.method,
				test.pathID,
				test.authorization,
				`{"kind":"identity_document"}`,
			)
			assertArtifactUploadIntentError(
				t,
				response,
				test.wantStatus,
				test.wantCode,
				test.wantMessage,
			)
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Errorf("Allow = %q, want %q", got, test.wantAllow)
			}
			if service.calls != 0 {
				t.Errorf("Create() calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestCreateIdentityDocumentUploadIntentMapsBoundedServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:       "invalid resume token",
			serviceErr: &session.Error{Code: session.CodeInvalidResumeToken},
			wantStatus: http.StatusUnauthorized, wantCode: "INVALID_RESUME_TOKEN",
			wantMessage: "invalid resume token",
		},
		{
			name:       "session not found",
			serviceErr: &session.Error{Code: session.CodeSessionNotFound},
			wantStatus: http.StatusNotFound, wantCode: "SESSION_NOT_FOUND",
			wantMessage: "session 4dbfda8d-f69e-453f-a1c4-2dba229fc73b not found",
		},
		{
			name:       "session expired",
			serviceErr: &session.Error{Code: session.CodeSessionExpired},
			wantStatus: http.StatusGone, wantCode: "SESSION_EXPIRED",
			wantMessage: "verification session expired",
		},
		{
			name:       "object storage unavailable",
			serviceErr: &artifact.Error{Code: artifact.CodeObjectStorageFailed},
			wantStatus: http.StatusServiceUnavailable, wantCode: "OBJECT_STORAGE_FAILED",
			wantMessage: "object storage is temporarily unavailable",
		},
		{
			name:       "unknown error",
			serviceErr: errors.New("database password must stay private"),
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL",
			wantMessage: "internal server error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newArtifactUploadIntentHTTPFixture()
			service.err = test.serviceErr
			response := performArtifactUploadIntentRequest(
				t,
				service,
				http.MethodPost,
				"4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
				"Bearer opaque-token",
				`{"kind":"identity_document"}`,
			)
			assertArtifactUploadIntentError(
				t,
				response,
				test.wantStatus,
				test.wantCode,
				test.wantMessage,
			)
			if service.calls != 1 {
				t.Errorf("Create() calls = %d, want 1", service.calls)
			}
			if strings.Contains(response.Body.String(), "database password") {
				t.Errorf("response leaks internal error: %s", response.Body.String())
			}
		})
	}
}

func newArtifactUploadIntentHTTPFixture() *artifactUploadIntentServiceStub {
	return &artifactUploadIntentServiceStub{
		created: artifact.CreatedUploadIntent{
			ID:        uuid.MustParse("21b15c5e-d8f3-4b79-ac47-d4f5fd55ac4c"),
			UploadURL: "https://public-object-host/upload",
		},
	}
}

func performArtifactUploadIntentRequest(
	t *testing.T,
	service *artifactUploadIntentServiceStub,
	method string,
	pathID string,
	authorization string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		method,
		"/verification-sessions/"+pathID+"/artifacts/upload-url",
		strings.NewReader(body),
	)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	httpapi.NewHandler(nil, nil, nil, service).ServeHTTP(response, request)
	return response
}

func assertArtifactUploadIntentError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantMessage string,
) {
	t.Helper()
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
	if apiError.Details != nil &&
		(len(apiError.Details) != 1 ||
			apiError.Details["id"] != "4dbfda8d-f69e-453f-a1c4-2dba229fc73b") {
		t.Errorf("error details = %#v, want nil or the existing bounded session id", apiError.Details)
	}
}
