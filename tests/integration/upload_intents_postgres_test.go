package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestUploadIntentMigrationAndConstraintProof(t *testing.T) {
	ctx, container, _ := openUploadIntentDatabase(t)

	output := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/005_upload_intent_constraints.sql",
		"/tmp/005_upload_intent_constraints.sql",
	)

	t.Logf("Upload Intent SQL proof output:\n%s", output)
}

func TestConcurrentPendingUploadIntentCreationAllowsExactlyOneWinner(t *testing.T) {
	ctx, _, databaseURL := openUploadIntentDatabase(t)
	control, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect control database: %v", err)
	}
	t.Cleanup(func() { _ = control.Close(context.Background()) })

	var sessionID uuid.UUID
	err = control.QueryRow(
		ctx,
		`INSERT INTO verification_sessions (resume_token_hash, expires_at)
		 VALUES (sha256(convert_to($1, 'UTF8')), $2)
		 RETURNING id`,
		"concurrent-upload-intent-"+uuid.NewString(),
		time.Now().Add(30*time.Minute),
	).Scan(&sessionID)
	if err != nil {
		t.Fatalf("create verification session: %v", err)
	}

	connections := openCompetingConnections(t, ctx, databaseURL, 2)
	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(len(connections))
	results := make(chan error, len(connections))

	for _, connection := range connections {
		connection := connection
		go func() {
			intentID := uuid.New()
			ready.Done()
			<-start
			_, insertErr := connection.Exec(
				ctx,
				`INSERT INTO upload_intents (
					id,
					verification_session_id,
					kind,
					storage_key,
					expires_at
				) VALUES ($1, $2, 'identity_document', $3, $4)`,
				intentID,
				sessionID,
				"verification-sessions/"+sessionID.String()+"/identity_document/"+intentID.String(),
				time.Now().Add(5*time.Minute),
			)
			results <- insertErr
		}()
	}

	ready.Wait()
	close(start)

	successCount := 0
	conflictCount := 0
	for range connections {
		insertErr := <-results
		if insertErr == nil {
			successCount++
			continue
		}

		var postgresError *pgconn.PgError
		if !errors.As(insertErr, &postgresError) {
			t.Fatalf("competing insert error = %T %v, want PostgreSQL unique violation", insertErr, insertErr)
		}
		if postgresError.Code != "23505" {
			t.Fatalf("competing insert SQLSTATE = %s, want 23505: %v", postgresError.Code, postgresError)
		}
		if postgresError.ConstraintName != "upload_intents_one_pending_per_session_kind_idx" {
			t.Fatalf(
				"competing insert constraint = %q, want upload_intents_one_pending_per_session_kind_idx",
				postgresError.ConstraintName,
			)
		}
		conflictCount++
	}

	if successCount != 1 || conflictCount != 1 {
		t.Fatalf("competing insert results = %d success/%d conflict, want 1/1", successCount, conflictCount)
	}

	var pendingCount int
	err = control.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM upload_intents
		 WHERE verification_session_id = $1
		   AND kind = 'identity_document'
		   AND status = 'pending'`,
		sessionID,
	).Scan(&pendingCount)
	if err != nil {
		t.Fatalf("count committed pending intents: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("committed pending intent count = %d, want 1", pendingCount)
	}
}

func openUploadIntentDatabase(t *testing.T) (context.Context, *postgres.PostgresContainer, string) {
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

	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00001_create_verification_sessions.sql", "/tmp/00001.sql"},
		{"../../sql/migrations/00002_add_resume_token_authentication.sql", "/tmp/00002.sql"},
		{"../../sql/migrations/00003_create_session_events.sql", "/tmp/00003.sql"},
		{"../../sql/migrations/00004_create_personal_details.sql", "/tmp/00004.sql"},
		{"../../sql/migrations/00005_create_upload_intents.sql", "/tmp/00005.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL connection string: %v", err)
	}

	return ctx, container, databaseURL
}
