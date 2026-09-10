package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/session"
)

func TestProviderSubmissionInitialAndReplayHaveExactAcceptedContract(t *testing.T) {
	sessionID := uuid.MustParse("2e1a3cde-d369-4f7e-9701-751d20fb211c")

	for _, replayed := range []bool{false, true} {
		name := "initial"
		if replayed {
			name = "replay"
		}
		t.Run(name, func(t *testing.T) {
			service := &stubProviderSubmissionService{result: providersubmission.Result{
				ID:       sessionID,
				Status:   session.StatusVerificationPending,
				Replayed: replayed,
			}}
			request := httptest.NewRequest(
				http.MethodPost,
				"/verification-sessions/"+sessionID.String()+"/submit",
				nil,
			)
			request.Header.Set("Authorization", "Bearer opaque-resume-token")
			response := httptest.NewRecorder()

			httpapi.NewHandler(nil, nil, nil, nil, nil, service).ServeHTTP(response, request)

			if response.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			wantBody := `{"id":"` + sessionID.String() + `","status":"verification_pending"}` + "\n"
			if got := response.Body.String(); got != wantBody {
				t.Errorf("body = %q, want exact %q", got, wantBody)
			}
			if service.calls != 1 || service.id != sessionID || service.rawToken != "opaque-resume-token" {
				t.Errorf(
					"Submit() calls=%d id=%s token=%q, want one exact authenticated call",
					service.calls,
					service.id,
					service.rawToken,
				)
			}
		})
	}
}

// stubProviderSubmissionService is also the auth/error fixture for the post-review
// public failure matrix. Keep errors injectable without adding those cases before
// the user-owned success path is reviewed.
type stubProviderSubmissionService struct {
	result   providersubmission.Result
	err      error
	id       uuid.UUID
	rawToken string
	calls    int
}

func (s *stubProviderSubmissionService) Submit(
	_ context.Context,
	id uuid.UUID,
	rawToken string,
) (providersubmission.Result, error) {
	s.calls++
	s.id = id
	s.rawToken = rawToken
	return s.result, s.err
}
