package integration_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const postgresImage = "postgres:18.4-alpine3.23"

func TestVerificationSessionMigrationAndSQLProof(t *testing.T) {
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

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL connection string: %v", err)
	}
	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })

	migrationOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/migrations/00001_create_verification_sessions.sql",
		"/tmp/00001_create_verification_sessions.sql",
	)
	if migrationOutput == "" {
		t.Fatal("migration produced no psql output")
	}
	resumeMigrationOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/migrations/00002_add_resume_token_authentication.sql",
		"/tmp/00002_add_resume_token_authentication.sql",
	)
	if resumeMigrationOutput == "" {
		t.Fatal("resume-token migration produced no psql output")
	}
	eventMigrationOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/migrations/00003_create_session_events.sql",
		"/tmp/00003_create_session_events.sql",
	)
	if eventMigrationOutput == "" {
		t.Fatal("Session Event migration produced no psql output")
	}
	personalDetailsMigrationOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/migrations/00004_create_personal_details.sql",
		"/tmp/00004_create_personal_details.sql",
	)
	if personalDetailsMigrationOutput == "" {
		t.Fatal("Personal Details migration produced no psql output")
	}
	exerciseOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/exercises/001_insert_and_select_verification_session.sql",
		"/tmp/001_insert_and_select_verification_session.sql",
	)
	if exerciseOutput == "" {
		t.Fatal("SQL exercise produced no psql output")
	}
	t.Logf("SQL exercise output:\n%s", exerciseOutput)

	var id string
	var status string
	var createdAt time.Time
	var updatedAt time.Time
	err = database.QueryRow(ctx, `
		INSERT INTO verification_sessions (resume_token_hash, expires_at)
		VALUES (sha256(convert_to('integration-proof-token', 'UTF8')), now() + interval '30 minutes')
		RETURNING id::text, status, created_at, updated_at
	`).Scan(&id, &status, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("database-generated id = %q, want UUID: %v", id, err)
	}
	if status != "created" {
		t.Errorf("database-generated status = %q, want %q", status, "created")
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		t.Errorf("database-generated timestamps must be non-zero: created=%s updated=%s", createdAt, updatedAt)
	}

	_, err = database.Exec(ctx, `
		INSERT INTO verification_sessions (status, resume_token_hash, expires_at)
		VALUES (
			'unknown_public_state',
			sha256(convert_to('invalid-integration-status-token', 'UTF8')),
			now() + interval '30 minutes'
		)
	`)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
		t.Fatalf("unknown state error = %v, want PostgreSQL check violation 23514", err)
	}

	proofOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/001_verification_session_defaults.sql",
		"/tmp/verification_session_proof.sql",
	)
	t.Logf("SQL proof output:\n%s", proofOutput)
	resumeProofOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/002_resume_token_authentication.sql",
		"/tmp/002_resume_token_authentication.sql",
	)
	t.Logf("resume-token SQL proof output:\n%s", resumeProofOutput)
	eventProofOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/003_atomic_session_events.sql",
		"/tmp/003_atomic_session_events.sql",
	)
	t.Logf("atomic Session Event SQL proof output:\n%s", eventProofOutput)
	personalDetailsProofOutput := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/004_immutable_personal_details.sql",
		"/tmp/004_immutable_personal_details.sql",
	)
	t.Logf("immutable Personal Details SQL proof output:\n%s", personalDetailsProofOutput)
}

func TestSessionEventMigrationAdmitsExactlyTheSevenActionVerbs(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)

	var sessionID uuid.UUID
	err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (resume_token_hash, expires_at)
		VALUES (sha256(convert_to('event-type-proof-token', 'UTF8')), now() + interval '30 minutes')
		RETURNING id
	`).Scan(&sessionID)
	if err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	allowed := []string{
		"submit_personal_details",
		"confirm_identity_document",
		"confirm_biometric_capture",
		"submit_session",
		"verification_passed",
		"verification_failed",
		"expire",
	}
	for _, eventType := range allowed {
		if _, err := database.Exec(ctx, `
			INSERT INTO session_events (session_id, event_type)
			VALUES ($1, $2)
		`, sessionID, eventType); err != nil {
			t.Errorf("insert allowed event type %q: %v", eventType, err)
		}
	}

	for _, eventType := range []string{"unknown", "personal_details_submitted", "verified", "expired"} {
		_, err := database.Exec(ctx, `
			INSERT INTO session_events (session_id, event_type)
			VALUES ($1, $2)
		`, sessionID, eventType)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
			t.Errorf("insert rejected event type %q error = %v, want check violation 23514", eventType, err)
		}
	}
}

func TestSessionEventMigrationRejectsUnboundedOrSensitiveMetadata(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)

	var sessionID uuid.UUID
	err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (resume_token_hash, expires_at)
		VALUES (sha256(convert_to('metadata-proof-token', 'UTF8')), now() + interval '30 minutes')
		RETURNING id
	`).Scan(&sessionID)
	if err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	for _, metadata := range []string{
		`{"personal_details":{"name":"Applicant"}}`,
		`{"resume_token":"secret"}`,
		`{"object_url":"https://storage.invalid/private"}`,
		`{"identity_number":"123"}`,
		`{"address":"private"}`,
		`{"raw_extraction":{"identity_number":"123"}}`,
		`{"provider_body":{"result":"raw"}}`,
	} {
		_, err := database.Exec(ctx, `
			INSERT INTO session_events (session_id, event_type, metadata)
			VALUES ($1, 'confirm_identity_document', $2::jsonb)
		`, sessionID, metadata)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
			t.Errorf("insert metadata %s error = %v, want check violation 23514", metadata, err)
		}
	}

	for _, metadata := range []string{
		`{}`,
		`{"outcome":"accepted"}`,
		`{"outcome":"local_validation_failed"}`,
	} {
		if _, err := database.Exec(ctx, `
			INSERT INTO session_events (session_id, event_type, metadata)
			VALUES ($1, 'confirm_identity_document', $2::jsonb)
		`, sessionID, metadata); err != nil {
			t.Errorf("insert safe metadata %s: %v", metadata, err)
		}
	}
}

func TestSessionEventsCannotBeUpdatedOrDeleted(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)

	var eventID uuid.UUID
	err := database.QueryRow(ctx, `
		WITH inserted_session AS (
			INSERT INTO verification_sessions (resume_token_hash, expires_at)
			VALUES (sha256(convert_to('append-only-proof-token', 'UTF8')), now() + interval '30 minutes')
			RETURNING id
		)
		INSERT INTO session_events (session_id, event_type)
		SELECT id, 'submit_personal_details' FROM inserted_session
		RETURNING id
	`).Scan(&eventID)
	if err != nil {
		t.Fatalf("insert Session Event: %v", err)
	}

	for name, statement := range map[string]string{
		"update": `UPDATE session_events SET metadata = '{"outcome":"accepted"}' WHERE id = $1`,
		"delete": `DELETE FROM session_events WHERE id = $1`,
	} {
		_, err := database.Exec(ctx, statement, eventID)
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
			t.Errorf("%s Session Event error = %v, want object-not-in-prerequisite-state 55000", name, err)
		}
	}
}

func TestPostgresEventTransactionCommitsOneStateChangeAndOneOrderedEvent(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)

	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	sessions := postgresadapter.NewSessionStore(database)
	created, err := sessions.Create(ctx, session.CreateParams{
		ResumeTokenHash: []byte("12345678901234567890123456789012"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	transactions := postgresadapter.NewEventTransactions(database)
	service := session.NewEventService(transactions, integrationClock{now: now})

	if err := service.RecordPersonalDetailsSubmission(ctx, created.ID); err != nil {
		t.Fatalf("RecordPersonalDetailsSubmission() error = %v", err)
	}

	updated, err := sessions.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read updated Verification Session: %v", err)
	}
	if updated.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf("status = %q, want %q", updated.Status, session.StatusPersonalDetailsSubmitted)
	}
	events, err := transactions.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want exactly 1", len(events))
	}
	if events[0].Type != sessionevent.SubmitPersonalDetails || !events[0].OccurredAt.Equal(now) {
		t.Errorf("event = %#v, want submit_personal_details at fixed time", events[0])
	}
}

func TestPostgresEventTransactionRollsBackStateWhenOperationFails(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)

	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	sessions := postgresadapter.NewSessionStore(database)
	created, err := sessions.Create(ctx, session.CreateParams{
		ResumeTokenHash: []byte("abcdefghijklmnopqrstuvwxyz123456"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}
	transactions := postgresadapter.NewEventTransactions(database)
	forcedFailure := errors.New("forced failure after guarded update")

	err = transactions.WithinTransaction(ctx, func(tx session.EventTransaction) error {
		if err := tx.UpdateState(
			ctx,
			created.ID,
			verificationsession.Created,
			verificationsession.PersonalDetailsSubmitted,
			now,
		); err != nil {
			return err
		}
		return forcedFailure
	})
	if !errors.Is(err, forcedFailure) {
		t.Fatalf("WithinTransaction() error = %v, want forced failure", err)
	}

	unchanged, err := sessions.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read Verification Session after rollback: %v", err)
	}
	if unchanged.Status != session.StatusCreated {
		t.Errorf("status after rollback = %q, want %q", unchanged.Status, session.StatusCreated)
	}
	events, err := transactions.ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListEvents() after rollback error = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events after rollback = %d, want 0", len(events))
	}
}

func TestPostgresExpectedStateGuardAllowsOnlyOneCompetingTransition(t *testing.T) {
	ctx, database := openSessionEventDatabase(t)
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	sessions := postgresadapter.NewSessionStore(database)
	created, err := sessions.Create(ctx, session.CreateParams{
		ResumeTokenHash: []byte("12345678901234567890123456789012"),
		ExpiresAt:       now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create Verification Session: %v", err)
	}

	databaseURL := database.Config().ConnString()
	connections := make([]*pgx.Conn, 2)
	for index := range connections {
		connections[index], err = pgx.Connect(ctx, databaseURL)
		if err != nil {
			t.Fatalf("open competing connection %d: %v", index+1, err)
		}
		connection := connections[index]
		t.Cleanup(func() { _ = connection.Close(context.Background()) })
	}

	start := make(chan struct{})
	results := make(chan error, len(connections))
	var ready sync.WaitGroup
	ready.Add(len(connections))
	for _, connection := range connections {
		go func(connection *pgx.Conn) {
			service := session.NewEventService(postgresadapter.NewEventTransactions(connection), integrationClock{now: now})
			ready.Done()
			<-start
			results <- service.Transition(ctx, session.TransitionParams{
				SessionID:     created.ID,
				ExpectedState: verificationsession.Created,
				Action:        sessionevent.SubmitPersonalDetails,
			})
		}(connection)
	}
	ready.Wait()
	close(start)

	wins := 0
	stale := 0
	for range connections {
		result := <-results
		switch {
		case result == nil:
			wins++
		case errors.Is(result, session.ErrSessionTransitionStale):
			stale++
		default:
			t.Fatalf("competing transition error = %v, want nil or stale", result)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("competing outcomes = %d wins, %d stale; want exactly one each", wins, stale)
	}

	updated, err := sessions.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("read winning state: %v", err)
	}
	if updated.Status != verificationsession.PersonalDetailsSubmitted {
		t.Errorf("winning state = %q, want %q", updated.Status, verificationsession.PersonalDetailsSubmitted)
	}
	events, err := postgresadapter.NewEventTransactions(database).ListEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("list winning Session Events: %v", err)
	}
	if len(events) != 1 || events[0].Type != sessionevent.SubmitPersonalDetails {
		t.Errorf("winning events = %#v, want exactly one submit_personal_details", events)
	}
}

func TestSessionStorePreservesPostgresContextCancellation(t *testing.T) {
	_, database := openSessionEventDatabase(t)
	store := postgresadapter.NewSessionStore(database)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.FindByID(cancelled, uuid.New())

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FindByID() error = %v, want context.Canceled", err)
	}
}

func openSessionEventDatabase(t *testing.T) (context.Context, *pgx.Conn) {
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

func runPSQLFile(
	t *testing.T,
	ctx context.Context,
	container *postgres.PostgresContainer,
	hostPath string,
	containerPath string,
) string {
	t.Helper()

	absolutePath, err := filepath.Abs(hostPath)
	if err != nil {
		t.Fatalf("resolve SQL file path: %v", err)
	}
	if err := container.CopyFileToContainer(ctx, absolutePath, containerPath, 0o644); err != nil {
		t.Fatalf("copy SQL file: %v", err)
	}
	exitCode, output, err := container.Exec(ctx, []string{
		"psql", "-v", "ON_ERROR_STOP=1", "-U", "lawang", "-d", "lawang_test",
		"-f", containerPath,
	})
	if err != nil {
		t.Fatalf("execute SQL file: %v", err)
	}
	fileOutput, readErr := io.ReadAll(output)
	if readErr != nil {
		t.Fatalf("read SQL file output: %v", readErr)
	}
	if exitCode != 0 {
		t.Fatalf("psql exit code = %d, output:\n%s", exitCode, fileOutput)
	}
	return string(fileOutput)
}
