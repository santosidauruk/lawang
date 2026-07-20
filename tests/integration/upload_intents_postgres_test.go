package integration_test

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Checkpoint 1 (user-authored): add the first real concurrent Upload Intent
// creation test here after migration 00005 and its SQL constraint proof are
// complete. Use separate PostgreSQL connections, coordinate their start without
// timing sleeps, and assert the committed database outcome rather than internal
// adapter calls.

func TestUploadIntentMigrationAndConstraintProof(t *testing.T) {
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

	migrations := []struct {
		hostPath      string
		containerPath string
	}{
		{
			hostPath:      "../../sql/migrations/00001_create_verification_sessions.sql",
			containerPath: "/tmp/00001.sql",
		},
		{
			hostPath:      "../../sql/migrations/00002_add_resume_token_authentication.sql",
			containerPath: "/tmp/00002.sql",
		},
		{
			hostPath:      "../../sql/migrations/00003_create_session_events.sql",
			containerPath: "/tmp/00003.sql",
		},
		{
			hostPath:      "../../sql/migrations/00004_create_personal_details.sql",
			containerPath: "/tmp/00004.sql",
		},
		{
			hostPath:      "../../sql/migrations/00005_create_upload_intents.sql",
			containerPath: "/tmp/00005.sql",
		},
	}

	for _, migration := range migrations {
		runPSQLFile(
			t,
			ctx,
			container,
			migration.hostPath,
			migration.containerPath,
		)
	}

	output := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/005_upload_intent_constraints.sql",
		"/tmp/005_upload_intent_constraints.sql",
	)

	t.Logf("Output Intent SQL Proof output:\n%s", output)

}
