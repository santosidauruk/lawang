package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPersonalDetailsMigrationEnforcesOneToOneImmutableShape(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)

	var sessionID uuid.UUID
	err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (resume_token_hash, expires_at)
		VALUES (sha256(convert_to('personal-details-token', 'UTF8')), now() + interval '30 minutes')
		RETURNING id
	`).Scan(&sessionID)
	if err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	var dateOfBirth time.Time
	var fullName, identityNumber, address string
	err = database.QueryRow(ctx, `
		INSERT INTO personal_details (
			verification_session_id, full_name, date_of_birth, identity_number, address
		) VALUES ($1, '', DATE '2000-02-29', '', '')
		RETURNING full_name, date_of_birth, identity_number, address
	`, sessionID).Scan(&fullName, &dateOfBirth, &identityNumber, &address)
	if err != nil {
		t.Fatalf("insert Personal Details: %v", err)
	}
	if fullName != "" || identityNumber != "" || address != "" || dateOfBirth.Format("2006-01-02") != "2000-02-29" {
		t.Errorf("stored values = %q %s %q %q, want compatible empty text and calendar date", fullName, dateOfBirth, identityNumber, address)
	}

	_, err = database.Exec(ctx, `
		INSERT INTO personal_details (
			verification_session_id, full_name, date_of_birth, identity_number, address
		) VALUES ($1, 'Different', DATE '1990-01-02', 'different', 'different')
	`, sessionID)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		t.Fatalf("second Personal Details insert error = %v, want unique violation 23505", err)
	}
}

func TestPostgresPersonalDetailsSubmitCommitsDetailsStateAndSafeEvent(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	tokens := session.NewProductionCryptoTokens()
	stored, err := postgresadapter.NewSessionStore(database).Create(ctx, session.CreateParams{
		ResumeTokenHash: tokens.Hash("raw-resume-token"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	service := personaldetails.NewService(
		postgresadapter.NewPersonalDetailsTransactions(database),
		tokens,
		integrationClock{now: now},
	)

	got, err := service.Submit(ctx, stored.ID, "raw-resume-token", personaldetails.Input{
		FullName: "Alice Applicant", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "1234567890", Address: "Jalan Perjuangan 1",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("Submit() status = %q, want %q", got.Status, session.StatusPersonalDetailsSubmitted)
	}

	updated, err := postgresadapter.NewSessionStore(database).FindByID(ctx, stored.ID)
	if err != nil {
		t.Fatalf("read updated Verification Session: %v", err)
	}
	if updated.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("stored state = %q, want %q", updated.Status, session.StatusPersonalDetailsSubmitted)
	}
	events, err := postgresadapter.NewEventTransactions(database).ListEvents(ctx, stored.ID)
	if err != nil {
		t.Fatalf("list Session Events: %v", err)
	}
	if len(events) != 1 || events[0].Type != sessionevent.SubmitPersonalDetails || events[0].Metadata.Outcome() != "" {
		t.Errorf("stored events = %#v, want one safe submit_personal_details event", events)
	}
}

func TestPostgresPersonalDetailsTransactionRollsBackAllThreeWrites(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	stored, err := postgresadapter.NewSessionStore(database).Create(ctx, session.CreateParams{
		ResumeTokenHash: session.NewProductionCryptoTokens().Hash("rollback-token"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	forcedFailure := errors.New("forced failure after all Personal Details writes")
	transactions := postgresadapter.NewPersonalDetailsTransactions(database)

	err = transactions.WithinTransaction(ctx, func(tx personaldetails.Transaction) error {
		if err := tx.InsertDetails(ctx, personaldetails.PersonalDetails{
			SessionID: stored.ID,
			Input: personaldetails.Input{
				FullName: "Alice", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC),
				IdentityNumber: "123", Address: "Somewhere",
			},
			CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.UpdateState(ctx, stored.ID, verificationsession.Created, verificationsession.PersonalDetailsSubmitted, now); err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID: stored.ID, Type: sessionevent.SubmitPersonalDetails,
			Metadata: sessionevent.EmptyMetadata(), OccurredAt: now,
		}); err != nil {
			return err
		}
		return forcedFailure
	})
	if !errors.Is(err, forcedFailure) {
		t.Fatalf("WithinTransaction() error = %v, want forced failure", err)
	}

	unchanged, err := postgresadapter.NewSessionStore(database).FindByID(ctx, stored.ID)
	if err != nil {
		t.Fatalf("read session after rollback: %v", err)
	}
	if unchanged.Status != session.StatusCreated {
		t.Errorf("state after rollback = %q, want %q", unchanged.Status, session.StatusCreated)
	}
	var detailCount, eventCount int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM personal_details WHERE verification_session_id = $1`, stored.ID).Scan(&detailCount); err != nil {
		t.Fatalf("count Personal Details: %v", err)
	}
	if err := database.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id = $1`, stored.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count Session Events: %v", err)
	}
	if detailCount != 0 || eventCount != 0 {
		t.Errorf("rollback left %d Personal Details and %d Session Events, want zero", detailCount, eventCount)
	}
}

func TestConcurrentIdenticalPersonalDetailsSubmissionsConverge(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	tokens := session.NewProductionCryptoTokens()
	stored, err := postgresadapter.NewSessionStore(database).Create(ctx, session.CreateParams{
		ResumeTokenHash: tokens.Hash("concurrent-identical-token"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	input := personaldetails.Input{
		FullName: "Alice", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC),
		IdentityNumber: "123", Address: "Somewhere",
	}

	connections := openCompetingConnections(t, ctx, database.Config().ConnString(), 2)
	start := make(chan struct{})
	results := make(chan error, len(connections))
	var ready sync.WaitGroup
	ready.Add(len(connections))
	for _, connection := range connections {
		go func(connection *pgx.Conn) {
			service := personaldetails.NewService(
				postgresadapter.NewPersonalDetailsTransactions(connection),
				tokens,
				integrationClock{now: now},
			)
			ready.Done()
			<-start
			_, submitErr := service.Submit(ctx, stored.ID, "concurrent-identical-token", input)
			results <- submitErr
		}(connection)
	}
	ready.Wait()
	close(start)
	for range connections {
		if err := <-results; err != nil {
			t.Fatalf("concurrent identical Submit() error = %v", err)
		}
	}

	var detailCount, eventCount int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM personal_details WHERE verification_session_id = $1`, stored.ID).Scan(&detailCount); err != nil {
		t.Fatalf("count Personal Details: %v", err)
	}
	if err := database.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id = $1`, stored.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count Session Events: %v", err)
	}
	if detailCount != 1 || eventCount != 1 {
		t.Errorf("concurrent identical submits left %d details and %d events, want one each", detailCount, eventCount)
	}
}

func TestConcurrentDifferentPersonalDetailsSubmissionsProduceOneConflict(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	tokens := session.NewProductionCryptoTokens()
	stored, err := postgresadapter.NewSessionStore(database).Create(ctx, session.CreateParams{
		ResumeTokenHash: tokens.Hash("concurrent-different-token"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	inputs := []personaldetails.Input{
		{FullName: "Alice", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), IdentityNumber: "123", Address: "Somewhere"},
		{FullName: "Bob", DateOfBirth: time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC), IdentityNumber: "123", Address: "Somewhere"},
	}
	connections := openCompetingConnections(t, ctx, database.Config().ConnString(), len(inputs))
	start := make(chan struct{})
	results := make(chan error, len(connections))
	var ready sync.WaitGroup
	ready.Add(len(connections))
	for index, connection := range connections {
		input := inputs[index]
		go func(connection *pgx.Conn) {
			service := personaldetails.NewService(
				postgresadapter.NewPersonalDetailsTransactions(connection), tokens, integrationClock{now: now},
			)
			ready.Done()
			<-start
			_, submitErr := service.Submit(ctx, stored.ID, "concurrent-different-token", input)
			results <- submitErr
		}(connection)
	}
	ready.Wait()
	close(start)

	successes, conflicts := 0, 0
	for range connections {
		result := <-results
		if result == nil {
			successes++
			continue
		}
		var serviceError *personaldetails.Error
		if errors.As(result, &serviceError) && serviceError.Code == personaldetails.CodeConflict {
			conflicts++
			continue
		}
		t.Fatalf("concurrent different Submit() error = %v, want nil or conflict", result)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes = %d success, %d conflict; want one each", successes, conflicts)
	}

	var detailCount, eventCount int
	if err := database.QueryRow(ctx, `SELECT count(*) FROM personal_details WHERE verification_session_id = $1`, stored.ID).Scan(&detailCount); err != nil {
		t.Fatalf("count Personal Details: %v", err)
	}
	if err := database.QueryRow(ctx, `SELECT count(*) FROM session_events WHERE session_id = $1`, stored.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count Session Events: %v", err)
	}
	if detailCount != 1 || eventCount != 1 {
		t.Errorf("concurrent different submits left %d details and %d events, want one each", detailCount, eventCount)
	}
}

func TestApplicantSubmitsPersonalDetailsOverHTTPWithPostgreSQL(t *testing.T) {
	ctx, database := openPersonalDetailsDatabase(t)
	_ = ctx
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	tokens := session.NewProductionCryptoTokens()
	clock := integrationClock{now: now}
	sessions := session.NewService(postgresadapter.NewSessionStore(database), tokens, clock)
	details := personaldetails.NewService(postgresadapter.NewPersonalDetailsTransactions(database), tokens, clock)
	handler := httpapi.NewHandler(sessions, details, nil, nil)

	createRequest := httptest.NewRequest(http.MethodPost, "/verification-sessions", nil)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body=%s", createResponse.Code, http.StatusCreated, createResponse.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		ResumeToken string `json:"resumeToken"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	submitRequest := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+created.ID+"/personal-details",
		strings.NewReader(`{"fullName":"Alice","dateOfBirth":"1990-01-02","identityNumber":"123","address":"Somewhere"}`),
	)
	submitRequest.Header.Set("Authorization", "Bearer "+created.ResumeToken)
	submitRequest.Header.Set("Content-Type", "application/json")
	submitResponse := httptest.NewRecorder()
	handler.ServeHTTP(submitResponse, submitRequest)

	if submitResponse.Code != http.StatusOK {
		t.Fatalf("submit status = %d, want %d; body=%s", submitResponse.Code, http.StatusOK, submitResponse.Body.String())
	}
	var submitted map[string]any
	if err := json.Unmarshal(submitResponse.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	want := map[string]any{
		"id": created.ID, "status": "personal_details_submitted", "expiresAt": now.Add(30 * time.Minute).Format(time.RFC3339),
	}
	if len(submitted) != len(want) {
		t.Fatalf("submit body = %#v, want exactly %#v", submitted, want)
	}
	for key, wantValue := range want {
		if submitted[key] != wantValue {
			t.Errorf("submit body[%q] = %#v, want %#v", key, submitted[key], wantValue)
		}
	}
}

func openCompetingConnections(t *testing.T, ctx context.Context, databaseURL string, count int) []*pgx.Conn {
	t.Helper()
	connections := make([]*pgx.Conn, count)
	for index := range connections {
		connection, err := pgx.Connect(ctx, databaseURL)
		if err != nil {
			t.Fatalf("open competing connection %d: %v", index+1, err)
		}
		connections[index] = connection
		t.Cleanup(func() { _ = connection.Close(context.Background()) })
	}
	return connections
}

func openPersonalDetailsDatabase(t *testing.T) (context.Context, *pgx.Conn) {
	t.Helper()
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
		{"../../sql/migrations/00004_create_personal_details.sql", "/tmp/00004.sql"},
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
	return ctx, database
}
