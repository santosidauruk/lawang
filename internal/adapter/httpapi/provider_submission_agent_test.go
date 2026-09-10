package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
)

func TestProviderSubmissionMapsAuthenticationStateAndInternalFailuresSafely(t *testing.T) {
	sessionID := uuid.MustParse("33a38550-9712-4c19-a560-b688ec2c762d")
	tests := []struct {
		name        string
		header      string
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails map[string]any
		wantCalls   int
	}{
		{name: "missing authorization", wantStatus: http.StatusUnauthorized, wantCode: "MISSING_AUTHORIZATION"},
		{name: "malformed authorization", header: "Bearer one two", wantStatus: http.StatusUnauthorized, wantCode: "MALFORMED_AUTHORIZATION"},
		{name: "wrong token", header: "Bearer wrong", serviceErr: &session.Error{Code: session.CodeInvalidResumeToken}, wantStatus: http.StatusUnauthorized, wantCode: "INVALID_RESUME_TOKEN", wantCalls: 1},
		{name: "unknown session", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionNotFound}, wantStatus: http.StatusNotFound, wantCode: "SESSION_NOT_FOUND", wantCalls: 1},
		{name: "expired before first submission", header: "Bearer token", serviceErr: &session.Error{Code: session.CodeSessionExpired}, wantStatus: http.StatusGone, wantCode: "SESSION_EXPIRED", wantCalls: 1},
		{
			name: "not ready", header: "Bearer token",
			serviceErr: &providersubmission.Error{Code: providersubmission.CodeSubmissionNotReady, ID: sessionID},
			wantStatus: http.StatusConflict, wantCode: "SUBMISSION_NOT_READY",
			wantMessage: "verification session is not ready for provider submission",
			wantDetails: map[string]any{"id": sessionID.String()}, wantCalls: 1,
		},
		{
			name: "illegal transition", header: "Bearer token",
			serviceErr: &providersubmission.Error{Code: providersubmission.CodeIllegalTransition, ID: sessionID, From: session.StatusVerified, Action: sessionevent.SubmitSession},
			wantStatus: http.StatusConflict, wantCode: "ILLEGAL_TRANSITION",
			wantMessage: "illegal transition from verified via submit_session",
			wantDetails: map[string]any{"id": sessionID.String(), "from": "verified", "event": "submit_session"},
			wantCalls:   1,
		},
		{name: "internal failure", header: "Bearer token", serviceErr: errors.New("database password must stay private"), wantStatus: http.StatusInternalServerError, wantCode: "INTERNAL", wantMessage: "internal server error", wantCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubProviderSubmissionService{err: test.serviceErr}
			request := newProviderSubmissionRequest(sessionID, nil)
			request.Header.Del("Authorization")
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()

			httpapi.NewHandler(nil, nil, nil, nil, nil, service).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			var body httpapi.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode || service.calls != test.wantCalls {
				t.Errorf("result = code:%q calls:%d, want code:%q calls:%d", body.Code, service.calls, test.wantCode, test.wantCalls)
			}
			if test.wantMessage != "" && body.Message != test.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, test.wantMessage)
			}
			for key, want := range test.wantDetails {
				if body.Details[key] != want {
					t.Errorf("details[%q] = %#v, want %#v", key, body.Details[key], want)
				}
			}
			if strings.Contains(response.Body.String(), "database password") {
				t.Fatalf("error response leaked internal failure: %s", response.Body.String())
			}
		})
	}
}

func TestProviderSubmissionRejectsMalformedRoutingAndNonEmptyBodyBeforeService(t *testing.T) {
	sessionID := uuid.MustParse("33a38550-9712-4c19-a560-b688ec2c762d")
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{
			name: "malformed session ID", method: http.MethodPost,
			path:       "/verification-sessions/not-a-uuid/submit",
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
		},
		{
			name: "unsupported method", method: http.MethodGet,
			path:       "/verification-sessions/" + sessionID.String() + "/submit",
			wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantAllow: http.MethodPost,
		},
		{
			name: "non-empty body", method: http.MethodPost,
			path: "/verification-sessions/" + sessionID.String() + "/submit", body: `{}`,
			wantStatus: http.StatusBadRequest, wantCode: "VALIDATION_ERROR",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubProviderSubmissionService{result: providersubmission.Result{
				ID: sessionID, Status: session.StatusVerificationPending,
			}}
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer opaque-token")
			response := httptest.NewRecorder()

			httpapi.NewHandler(nil, nil, nil, nil, nil, service).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if response.Header().Get("Allow") != test.wantAllow {
				t.Errorf("Allow = %q, want %q", response.Header().Get("Allow"), test.wantAllow)
			}
			var body httpapi.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Code != test.wantCode || service.calls != 0 {
				t.Errorf("result = code:%q calls:%d, want code:%q calls:0", body.Code, service.calls, test.wantCode)
			}
		})
	}
}

func TestProviderSubmissionBodyReadFailureAndRequestLoggingRemainSafe(t *testing.T) {
	sessionID := uuid.MustParse("33a38550-9712-4c19-a560-b688ec2c762d")
	t.Run("body read failure", func(t *testing.T) {
		service := &stubProviderSubmissionService{}
		request := newProviderSubmissionRequest(sessionID, nil)
		request.Body = &failingProviderSubmissionBody{err: errors.New("body reader secret")}
		response := httptest.NewRecorder()

		httpapi.NewHandler(nil, nil, nil, nil, nil, service).ServeHTTP(response, request)

		if response.Code != http.StatusInternalServerError || service.calls != 0 {
			t.Fatalf("read failure = status:%d calls:%d", response.Code, service.calls)
		}
		if strings.Contains(response.Body.String(), "body reader secret") {
			t.Fatalf("read failure leaked internal error: %s", response.Body.String())
		}
	})

	t.Run("request log redaction", func(t *testing.T) {
		const rawToken = "do-not-log-provider-submission-token"
		const internalMarker = "do-not-log-provider-submission-storage-error"
		service := &stubProviderSubmissionService{err: errors.New(internalMarker)}
		var logs bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&logs, nil))
		handler := httpapi.WithRequestLogging(
			httpapi.NewHandler(nil, nil, nil, nil, nil, service),
			logger,
		)
		request := newProviderSubmissionRequest(sessionID, nil)
		request.Header.Set("Authorization", "Bearer "+rawToken)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		var entry map[string]any
		if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
			t.Fatalf("decode request log: %v; output=%s", err, logs.String())
		}
		for key, want := range map[string]any{
			"method": http.MethodPost, "route": "/verification-sessions/{id}/submit",
			"status": float64(http.StatusInternalServerError), "error_code": "INTERNAL",
		} {
			if entry[key] != want {
				t.Errorf("log[%q] = %#v, want %#v", key, entry[key], want)
			}
		}
		for _, secret := range []string{rawToken, internalMarker, "Authorization"} {
			if strings.Contains(logs.String(), secret) || strings.Contains(response.Body.String(), secret) {
				t.Errorf("HTTP outcome exposed %q", secret)
			}
		}
	})
}

type failingProviderSubmissionBody struct{ err error }

func (b *failingProviderSubmissionBody) Read([]byte) (int, error) { return 0, b.err }
func (*failingProviderSubmissionBody) Close() error               { return nil }

func newProviderSubmissionRequest(sessionID uuid.UUID, body *strings.Reader) *http.Request {
	var requestBody *strings.Reader
	if body == nil {
		requestBody = strings.NewReader("")
	} else {
		requestBody = body
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/submit",
		requestBody,
	)
	request.Header.Set("Authorization", "Bearer opaque-token")
	return request
}
