package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestLiveHealthContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	httpapi.NewHandler(nil).ServeHTTP(response, request)

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

	httpapi.NewHandler(nil).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got, want := response.Header().Get("Allow"), "GET, HEAD"; got != want {
		t.Errorf("Allow = %q, want %q", got, want)
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

	httpapi.NewHandler(service).ServeHTTP(response, request)

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

	httpapi.NewHandler(service).ServeHTTP(response, request)

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

			httpapi.NewHandler(service).ServeHTTP(response, request)

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

type stubSessionService struct {
	created     session.CreatedSession
	summary     session.Summary
	resumeID    uuid.UUID
	resumeToken string
	resumeErr   error
}

func (s *stubSessionService) Create(context.Context) (session.CreatedSession, error) {
	return s.created, nil
}

func (s *stubSessionService) Resume(_ context.Context, id uuid.UUID, token string) (session.Summary, error) {
	s.resumeID = id
	s.resumeToken = token
	return s.summary, s.resumeErr
}
