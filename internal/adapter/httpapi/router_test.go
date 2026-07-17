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
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

func TestLiveHealthContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := response.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestLiveHealthRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/health/live", nil)
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil, nil).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got, want := response.Header().Get("Allow"), "GET, HEAD"; got != want {
		t.Errorf("Allow = %q, want %q", got, want)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if got, want := body.Code, "METHOD_NOT_ALLOWED"; got != want {
		t.Errorf("error code = %q, want %q", got, want)
	}
}

func TestSessionRoutesRejectUnsupportedMethods(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	tests := []struct {
		name      string
		method    string
		path      string
		wantAllow string
	}{
		{name: "create is POST only", method: http.MethodGet, path: "/verification-sessions", wantAllow: http.MethodPost},
		{name: "resume is GET only", method: http.MethodPost, path: "/verification-sessions/" + id.String(), wantAllow: "GET, HEAD"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()

			httpapi.NewHandler(&stubSessionService{}, nil).ServeHTTP(response, request)

			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
			if got := response.Header().Get("Allow"); got != test.wantAllow {
				t.Errorf("Allow = %q, want %q", got, test.wantAllow)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != "METHOD_NOT_ALLOWED" {
				t.Errorf("error code = %q, want METHOD_NOT_ALLOWED", body.Code)
			}
		})
	}
}

func TestCreateVerificationSessionHTTPContract(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	expiresAt := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	service := &stubSessionService{created: session.CreatedSession{
		ID: id, Status: session.StatusCreated, ExpiresAt: expiresAt, ResumeToken: "opaque-token",
	}}
	request := httptest.NewRequest(http.MethodPost, "/verification-sessions", nil)
	response := httptest.NewRecorder()

	httpapi.NewHandler(service, nil).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := map[string]any{
		"id": id.String(), "status": "created", "expiresAt": expiresAt.Format(time.RFC3339), "resumeToken": "opaque-token",
	}
	if len(body) != len(want) {
		t.Fatalf("body = %#v, want exactly %#v", body, want)
	}
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("body[%q] = %#v, want %#v", key, body[key], wantValue)
		}
	}
}

func TestResumeVerificationSessionHTTPContract(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	expiresAt := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	service := &stubSessionService{summary: session.Summary{
		ID: id, Status: session.StatusCreated, ExpiresAt: expiresAt,
	}}
	request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil)
	request.Header.Set("Authorization", "bEaReR opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(service, nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.resumeID != id || service.resumeToken != "opaque-token" {
		t.Errorf("Resume() called with id=%s token=%q", service.resumeID, service.resumeToken)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, exists := body["resumeToken"]; exists {
		t.Fatal("resume response exposed raw token")
	}
	want := map[string]any{
		"id": id.String(), "status": "created", "expiresAt": expiresAt.Format(time.RFC3339),
	}
	if len(body) != len(want) {
		t.Fatalf("body = %#v, want exactly %#v", body, want)
	}
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("body[%q] = %#v, want %#v", key, body[key], wantValue)
		}
	}
}

func TestSubmitPersonalDetailsHTTPContract(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	expiresAt := time.Date(2026, 7, 15, 9, 30, 0, 0, time.UTC)
	service := &stubPersonalDetailsService{summary: session.Summary{
		ID: id, Status: session.StatusPersonalDetailsSubmitted, ExpiresAt: expiresAt,
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(`{"fullName":"","dateOfBirth":"2000-02-29","identityNumber":"","address":""}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	wantInput := personaldetails.Input{
		FullName: "", DateOfBirth: time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "", Address: "",
	}
	if service.id != id || service.token != "opaque-token" || service.input != wantInput {
		t.Errorf("Submit() called with id=%s token=%q input=%#v", service.id, service.token, service.input)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := map[string]any{
		"id": id.String(), "status": "personal_details_submitted", "expiresAt": expiresAt.Format(time.RFC3339),
	}
	if len(body) != len(want) {
		t.Fatalf("body = %#v, want exactly %#v", body, want)
	}
	for key, wantValue := range want {
		if body[key] != wantValue {
			t.Errorf("body[%q] = %#v, want %#v", key, body[key], wantValue)
		}
	}
}

func TestSubmitPersonalDetailsRejectsMissingRequiredField(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(`{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123"}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "VALIDATION_ERROR" {
		t.Errorf("error code = %q, want VALIDATION_ERROR", body.Code)
	}
	if service.calls != 0 {
		t.Errorf("Submit() calls = %d, want 0", service.calls)
	}
}

func TestSubmitPersonalDetailsRejectsUnknownField(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(`{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123","address":"Somewhere","extra":"rejected"}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "VALIDATION_ERROR" || service.calls != 0 {
		t.Errorf("unknown field result = code %q, Submit calls %d", body.Code, service.calls)
	}
}

func TestSubmitPersonalDetailsPreservesLegacyJSONFramingFailures(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	valid := `{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123","address":"Somewhere"}`
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{"fullName":`},
		{name: "empty", body: ""},
		{name: "second JSON value", body: valid + ` {}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubPersonalDetailsService{}
			request := httptest.NewRequest(
				http.MethodPost,
				"/verification-sessions/"+id.String()+"/personal-details",
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer opaque-token")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != "INTERNAL" || body.Message != "Internal server error" {
				t.Errorf("error body = %#v, want legacy INTERNAL envelope", body)
			}
			if service.calls != 0 {
				t.Errorf("Submit() calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestSubmitPersonalDetailsMapsDifferentReplayToConflictEnvelope(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{err: &personaldetails.Error{
		Code: personaldetails.CodeConflict, ID: id,
	}}
	request := newPersonalDetailsRequest(id)
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "PERSONAL_DETAILS_CONFLICT" ||
		body.Message != "personal details already submitted with different values for session "+id.String() ||
		body.Details["id"] != id.String() {
		t.Errorf("conflict body = %#v, want frozen legacy envelope", body)
	}
}

func TestSubmitPersonalDetailsMapsWrongStateToIllegalTransitionEnvelope(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{err: &personaldetails.Error{
		Code: personaldetails.CodeIllegalTransition, ID: id,
		From: session.StatusPersonalDetailsSubmitted, Action: sessionevent.SubmitPersonalDetails,
	}}
	request := newPersonalDetailsRequest(id)
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Code != "ILLEGAL_TRANSITION" ||
		body.Message != "illegal transition from personal_details_submitted via submit_personal_details" ||
		body.Details["id"] != id.String() ||
		body.Details["from"] != "personal_details_submitted" ||
		body.Details["event"] != "submit_personal_details" {
		t.Errorf("illegal-transition body = %#v, want frozen legacy envelope", body)
	}
}

func TestSubmitPersonalDetailsValidationCompatibility(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	tests := []struct {
		name string
		body string
	}{
		{name: "null body", body: `null`},
		{name: "array body", body: `[]`},
		{name: "wrong text type", body: `{"fullName":1,"dateOfBirth":"1990-01-02","identityNumber":"123","address":"Somewhere"}`},
		{name: "invalid calendar date", body: `{"fullName":"Alice","dateOfBirth":"1990-02-30","identityNumber":"123","address":"Somewhere"}`},
		{name: "empty date", body: `{"fullName":"Alice","dateOfBirth":"","identityNumber":"123","address":"Somewhere"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubPersonalDetailsService{}
			request := httptest.NewRequest(
				http.MethodPost,
				"/verification-sessions/"+id.String()+"/personal-details",
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer opaque-token")
			response := httptest.NewRecorder()

			httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != "VALIDATION_ERROR" || service.calls != 0 {
				t.Errorf("validation result = code %q, Submit calls %d", body.Code, service.calls)
			}
		})
	}
}

func TestSubmitPersonalDetailsAuthenticationAndSessionErrors(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	tests := []struct {
		name       string
		header     string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "missing authorization", wantStatus: http.StatusUnauthorized, wantCode: "MISSING_AUTHORIZATION"},
		{name: "malformed authorization", header: "Bearer one two", wantStatus: http.StatusUnauthorized, wantCode: "MALFORMED_AUTHORIZATION"},
		{name: "wrong token", header: "Bearer wrong", serviceErr: &session.Error{Code: session.CodeInvalidResumeToken}, wantStatus: http.StatusUnauthorized, wantCode: "INVALID_RESUME_TOKEN"},
		{name: "unknown session", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionNotFound}, wantStatus: http.StatusNotFound, wantCode: "SESSION_NOT_FOUND"},
		{name: "expired session", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionExpired}, wantStatus: http.StatusGone, wantCode: "SESSION_EXPIRED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubPersonalDetailsService{err: test.serviceErr}
			request := newPersonalDetailsRequest(id)
			request.Header.Del("Authorization")
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()

			httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", body.Code, test.wantCode)
			}
		})
	}
}

func TestSubmitPersonalDetailsLimitsRequestBody(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{}
	body := `{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123","address":"` + strings.Repeat("x", 1<<20) + `"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	var errorBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errorBody.Code != "INTERNAL" || service.calls != 0 {
		t.Errorf("oversized result = code %q, Submit calls %d", errorBody.Code, service.calls)
	}
}

func TestPersonalDetailsRequestLoggingExcludesPIIAndCredentials(t *testing.T) {
	const (
		rawToken       = "do-not-log-personal-details-token"
		fullName       = "Do Not Log Applicant"
		identityNumber = "9988776655"
		address        = "Do Not Log Street 1"
	)
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubPersonalDetailsService{err: &personaldetails.Error{
		Code: personaldetails.CodeConflict, ID: id,
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := httpapi.WithRequestLogging(httpapi.NewHandler(&stubSessionService{}, service), logger)
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(`{"fullName":"`+fullName+`","dateOfBirth":"1990-01-02","identityNumber":"`+identityNumber+`","address":"`+address+`"}`),
	)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode request log: %v; output=%s", err, logs.String())
	}
	for key, want := range map[string]any{
		"method":     http.MethodPost,
		"route":      "/verification-sessions/{id}/personal-details",
		"status":     float64(http.StatusConflict),
		"error_code": "PERSONAL_DETAILS_CONFLICT",
	} {
		if got := entry[key]; got != want {
			t.Errorf("log[%q] = %#v, want %#v", key, got, want)
		}
	}
	for _, secret := range []string{rawToken, fullName, identityNumber, address, "Authorization"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("request log exposed %q: %s", secret, logs.String())
		}
	}
}

func TestPersonalDetailsRoutingErrors(t *testing.T) {
	service := &stubPersonalDetailsService{}
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{
			name: "malformed session id", method: http.MethodPost,
			path:       "/verification-sessions/not-a-uuid/personal-details",
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
		},
		{
			name: "unsupported method", method: http.MethodGet,
			path:       "/verification-sessions/4dbfda8d-f69e-453f-a1c4-2dba229fc73b/personal-details",
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantAllow: http.MethodPost,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
			request.Header.Set("Authorization", "Bearer token")
			response := httptest.NewRecorder()

			httpapi.NewHandler(&stubSessionService{}, service).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if response.Header().Get("Allow") != test.wantAllow {
				t.Errorf("Allow = %q, want %q", response.Header().Get("Allow"), test.wantAllow)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", body.Code, test.wantCode)
			}
		})
	}
}

func newPersonalDetailsRequest(id uuid.UUID) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+id.String()+"/personal-details",
		strings.NewReader(`{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123","address":"Somewhere"}`),
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestResumeVerificationSessionErrorContract(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	tests := []struct {
		name       string
		header     string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{name: "missing authorization", wantStatus: http.StatusUnauthorized, wantCode: "MISSING_AUTHORIZATION"},
		{name: "malformed authorization", header: "Bearer one two", wantStatus: http.StatusUnauthorized, wantCode: "MALFORMED_AUTHORIZATION"},
		{name: "wrong token", header: "Bearer wrong", serviceErr: &session.Error{Code: session.CodeInvalidResumeToken}, wantStatus: http.StatusUnauthorized, wantCode: "INVALID_RESUME_TOKEN"},
		{name: "unknown session", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionNotFound}, wantStatus: http.StatusNotFound, wantCode: "SESSION_NOT_FOUND"},
		{name: "expired session", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionExpired}, wantStatus: http.StatusGone, wantCode: "SESSION_EXPIRED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubSessionService{resumeErr: test.serviceErr}
			request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()

			httpapi.NewHandler(service, nil).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", body.Code, test.wantCode)
			}
		})
	}
}

func TestHTTPAdapterMapsMalformedAndInternalFailuresToSafeEnvelopes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		header     string
		service    *stubSessionService
		wantStatus int
		wantCode   string
	}{
		{
			name: "malformed session id", method: http.MethodGet, path: "/verification-sessions/not-a-uuid",
			header: "Bearer opaque-token", service: &stubSessionService{},
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
		},
		{
			name: "resume storage failure", method: http.MethodGet,
			path:   "/verification-sessions/4dbfda8d-f69e-453f-a1c4-2dba229fc73b",
			header: "Bearer opaque-token", service: &stubSessionService{resumeErr: errors.New("pgx secret failure")},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL",
		},
		{
			name: "create storage failure", method: http.MethodPost, path: "/verification-sessions",
			service:    &stubSessionService{createErr: errors.New("pgx secret failure")},
			wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()

			httpapi.NewHandler(test.service, nil).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode {
				t.Errorf("error code = %q, want %q", body.Code, test.wantCode)
			}
			if strings.Contains(response.Body.String(), "pgx secret failure") {
				t.Fatalf("error body exposed storage failure: %s", response.Body.String())
			}
		})
	}
}

func TestRequestLoggingRecordsSafeHTTPOutcomeWithoutCredentials(t *testing.T) {
	const rawToken = "do-not-log-this-resume-token"
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &stubSessionService{resumeErr: errors.New("database password must stay private")}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := httpapi.WithRequestLogging(httpapi.NewHandler(service, nil), logger)
	request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil)
	request.Header.Set("Authorization", "Bearer "+rawToken)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("X-Request-ID is empty")
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode request log: %v; output=%s", err, logs.String())
	}
	for key, want := range map[string]any{
		"request_id": requestID,
		"method":     http.MethodGet,
		"route":      "/verification-sessions/{id}",
		"status":     float64(http.StatusInternalServerError),
		"error_code": "INTERNAL",
	} {
		if got := entry[key]; got != want {
			t.Errorf("log[%q] = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := entry["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %#v, want number", entry["duration_ms"])
	}
	var errorBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &errorBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errorBody.Code != "INTERNAL" || errorBody.Message != "internal server error" {
		t.Errorf("error response = %#v, want safe INTERNAL envelope", errorBody)
	}
	if strings.Contains(logs.String(), rawToken) || strings.Contains(logs.String(), "Authorization") || strings.Contains(logs.String(), "database password") {
		t.Fatalf("request log exposed credentials or internal error: %s", logs.String())
	}
}

func TestRequestLoggingRecordsAuthenticationErrorCode(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := httpapi.WithRequestLogging(httpapi.NewHandler(&stubSessionService{}, nil), logger)
	request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode request log: %v", err)
	}
	if got, want := entry["error_code"], "MISSING_AUTHORIZATION"; got != want {
		t.Errorf("error_code = %#v, want %#v", got, want)
	}
}

func TestResumePropagatesRequestCancellationToApplication(t *testing.T) {
	id := uuid.MustParse("4dbfda8d-f69e-453f-a1c4-2dba229fc73b")
	service := &contextSessionService{}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil).WithContext(cancelled)
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(service, nil).ServeHTTP(response, request)

	if !errors.Is(service.resumeContextError, context.Canceled) {
		t.Fatalf("Resume() context error = %v, want context.Canceled", service.resumeContextError)
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

type stubSessionService struct {
	created     session.CreatedSession
	createErr   error
	summary     session.Summary
	resumeID    uuid.UUID
	resumeToken string
	resumeErr   error
}

func (s *stubSessionService) Create(context.Context) (session.CreatedSession, error) {
	return s.created, s.createErr
}

func (s *stubSessionService) Resume(_ context.Context, id uuid.UUID, token string) (session.Summary, error) {
	s.resumeID = id
	s.resumeToken = token
	return s.summary, s.resumeErr
}

type contextSessionService struct {
	resumeContextError error
}

type stubPersonalDetailsService struct {
	summary session.Summary
	err     error
	id      uuid.UUID
	token   string
	input   personaldetails.Input
	calls   int
}

func (s *stubPersonalDetailsService) Submit(
	_ context.Context,
	id uuid.UUID,
	token string,
	input personaldetails.Input,
) (session.Summary, error) {
	s.calls++
	s.id = id
	s.token = token
	s.input = input
	return s.summary, s.err
}

func (service *contextSessionService) Create(ctx context.Context) (session.CreatedSession, error) {
	return session.CreatedSession{}, ctx.Err()
}

func (service *contextSessionService) Resume(ctx context.Context, _ uuid.UUID, _ string) (session.Summary, error) {
	service.resumeContextError = ctx.Err()
	return session.Summary{}, ctx.Err()
}
