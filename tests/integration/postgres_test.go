package integration_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		INSERT INTO verification_sessions DEFAULT VALUES
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

	_, err = database.Exec(ctx, "INSERT INTO verification_sessions (status) VALUES ('unknown_public_state')")
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
