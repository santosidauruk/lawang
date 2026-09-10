package integration_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/session"
)

func TestConcurrentProviderSubmissionsOverHTTPConvergeToOneDurableOutcome(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create concurrent Provider Submission pool: %v", err)
	}
	t.Cleanup(pool.Close)
	service := providersubmission.NewService(
		postgresadapter.NewProviderSubmissionTransactions(pool),
		session.NewProductionCryptoTokens(),
		fixedClock{now: fixture.now},
	)
	handler := httpapi.NewHandler(nil, nil, nil, nil, nil, service)

	type result struct {
		status int
		body   string
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			request := httptest.NewRequest(
				http.MethodPost,
				"/verification-sessions/"+fixture.sessionID.String()+"/submit",
				nil,
			)
			request.Header.Set("Authorization", "Bearer "+fixture.rawToken)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			results <- result{status: response.Code, body: response.Body.String()}
		}()
	}
	ready.Wait()
	close(start)

	wantBody := `{"id":"` + fixture.sessionID.String() + `","status":"verification_pending"}` + "\n"
	for range 2 {
		result := <-results
		if result.status != http.StatusAccepted || result.body != wantBody {
			t.Errorf("concurrent response = status:%d body:%q", result.status, result.body)
		}
	}

	var status string
	var deadline time.Time
	var submitEvents, outboxRows int
	if err := database.QueryRow(ctx, `
		SELECT status, verification_deadline_at,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'submit_session'),
			(SELECT count(*) FROM outbox WHERE verification_session_id = $1 AND task_type = 'provider:submit')
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&status, &deadline, &submitEvents, &outboxRows); err != nil {
		t.Fatalf("read concurrent HTTP Provider Submission outcome: %v", err)
	}
	if status != "verification_pending" ||
		!deadline.Equal(fixture.now.Add(24*time.Hour)) ||
		submitEvents != 1 || outboxRows != 1 {
		t.Errorf(
			"concurrent durable outcome = status:%q deadline:%s events:%d outbox:%d",
			status,
			deadline,
			submitEvents,
			outboxRows,
		)
	}
}
