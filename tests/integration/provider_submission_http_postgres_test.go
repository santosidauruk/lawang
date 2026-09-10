package integration_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/session"
)

func TestProviderSubmissionInitialAndReplayOverHTTPWithPostgreSQL(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	service := providersubmission.NewService(
		postgresadapter.NewProviderSubmissionTransactions(database),
		session.NewProductionCryptoTokens(),
		fixedClock{now: fixture.now},
	)
	handler := httpapi.NewHandler(nil, nil, nil, nil, nil, service)
	wantBody := `{"id":"` + fixture.sessionID.String() + `","status":"verification_pending"}` + "\n"

	for _, requestName := range []string{"initial", "replay"} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/verification-sessions/"+fixture.sessionID.String()+"/submit",
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+fixture.rawToken)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if response.Code != http.StatusAccepted || response.Body.String() != wantBody {
			t.Fatalf(
				"%s response = status:%d body:%q, want status:%d body:%q",
				requestName,
				response.Code,
				response.Body.String(),
				http.StatusAccepted,
				wantBody,
			)
		}
	}

	var status string
	var deadline, applicantExpiresAt time.Time
	var submitEvents, outboxRows int
	if err := database.QueryRow(ctx, `
		SELECT status, verification_deadline_at, expires_at,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'submit_session'),
			(SELECT count(*) FROM outbox WHERE verification_session_id = $1 AND task_type = 'provider:submit')
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(
		&status,
		&deadline,
		&applicantExpiresAt,
		&submitEvents,
		&outboxRows,
	); err != nil {
		t.Fatalf("read HTTP Provider Submission outcome: %v", err)
	}
	if status != "verification_pending" ||
		!deadline.Equal(fixture.now.Add(24*time.Hour)) ||
		!applicantExpiresAt.Equal(fixture.applicantExpiresAt) ||
		submitEvents != 1 ||
		outboxRows != 1 {
		t.Errorf(
			"durable outcome = status:%q deadline:%s applicant-expiry:%s events:%d outbox:%d",
			status,
			deadline,
			applicantExpiresAt,
			submitEvents,
			outboxRows,
		)
	}
}
