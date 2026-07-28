package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestApplicantCreatesAndResumesSessionOverHTTPWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("lawang_test"),
		postgres.WithUsername("lawang"),
		postgres.WithPassword("lawang"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start disposable PostgreSQL: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	for _, migration := range []struct{ host, container string }{
		{"../../sql/migrations/00001_create_verification_sessions.sql", "/tmp/00001.sql"},
		{"../../sql/migrations/00002_add_resume_token_authentication.sql", "/tmp/00002.sql"},
		{"../../sql/migrations/00003_create_session_events.sql", "/tmp/00003.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.host, migration.container)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL connection string: %v", err)
	}
	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })

	now := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	store := postgresadapter.NewSessionStore(database)
	service := session.NewService(store, session.NewProductionCryptoTokens(), integrationClock{now: now})
	server := httptest.NewServer(httpapi.NewHandler(service, nil, nil, nil))
	t.Cleanup(server.Close)

	createResponse, err := http.Post(server.URL+"/verification-sessions", "", nil)
	if err != nil {
		t.Fatalf("POST Verification Session: %v", err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}
	var created struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		ExpiresAt   string `json:"expiresAt"`
		ResumeToken string `json:"resumeToken"`
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if created.ResumeToken == "" {
		t.Fatal("POST returned an empty resume token")
	}

	request, err := http.NewRequest(http.MethodGet, server.URL+"/verification-sessions/"+created.ID, nil)
	if err != nil {
		t.Fatalf("build GET request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+created.ResumeToken)
	resumeResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("GET Verification Session: %v", err)
	}
	defer resumeResponse.Body.Close()
	if resumeResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", resumeResponse.StatusCode, http.StatusOK)
	}
	var resumed map[string]any
	if err := json.NewDecoder(resumeResponse.Body).Decode(&resumed); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if resumed["id"] != created.ID || resumed["status"] != created.Status || resumed["expiresAt"] != created.ExpiresAt {
		t.Errorf("GET body = %#v, want current POST summary", resumed)
	}
	if _, exists := resumed["resumeToken"]; exists {
		t.Fatal("GET response exposed the raw resume token")
	}
}

func TestCancelledHTTPRequestReachesPostgreSQLAndReturnsSafeError(t *testing.T) {
	_, database := openSessionEventDatabase(t)
	store := postgresadapter.NewSessionStore(database)
	service := session.NewService(store, session.NewProductionCryptoTokens(), integrationClock{})
	id := uuid.New()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/verification-sessions/"+id.String(), nil).WithContext(cancelled)
	request.Header.Set("Authorization", "Bearer opaque-token")
	response := httptest.NewRecorder()

	httpapi.NewHandler(service, nil, nil, nil).ServeHTTP(response, request)

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
	if body.Code != "INTERNAL" || body.Message != "internal server error" {
		t.Errorf("error body = %#v, want safe INTERNAL envelope", body)
	}
}

type integrationClock struct{ now time.Time }

func (c integrationClock) Now() time.Time { return c.now }
