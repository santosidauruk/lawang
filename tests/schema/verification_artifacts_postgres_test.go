package schema_test

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const postgresImage = "postgres:18.4-alpine3.23"

func TestVerificationArtifactMigrationAndConstraintProof(t *testing.T) {
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

	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00001_create_verification_sessions.sql", "/tmp/00001.sql"},
		{"../../sql/migrations/00002_add_resume_token_authentication.sql", "/tmp/00002.sql"},
		{"../../sql/migrations/00003_create_session_events.sql", "/tmp/00003.sql"},
		{"../../sql/migrations/00004_create_personal_details.sql", "/tmp/00004.sql"},
		{"../../sql/migrations/00005_create_upload_intents.sql", "/tmp/00005.sql"},
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
	} {
		runSchemaSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL connection string: %v", err)
	}
	database, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect disposable PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(context.Background()) })

	var tableExists bool
	if err := database.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name = 'verification_artifacts'
		)
	`).Scan(&tableExists); err != nil {
		t.Fatalf("inspect Verification Artifact table: %v", err)
	}
	if !tableExists {
		t.Fatal("verification_artifacts table does not exist after migration 00006")
	}

	output := runSchemaSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/006_verification_artifact_constraints.sql",
		"/tmp/006_verification_artifact_constraints.sql",
	)
	if !strings.Contains(output, "proof passed: Verification Artifact constraints") {
		t.Fatalf("constraint proof did not emit its completion marker:\n%s", output)
	}
	t.Logf("Verification Artifact constraint proof output:\n%s", output)
}

func runSchemaSQLFile(
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
	fileOutput, err := io.ReadAll(output)
	if err != nil {
		t.Fatalf("read SQL file output: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("psql exit code = %d, output:\n%s", exitCode, fileOutput)
	}
	return string(fileOutput)
}
