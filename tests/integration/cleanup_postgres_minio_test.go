package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	s3storageadapter "github.com/santosidauruk/lawang/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

func TestManualCleanupMarksAlreadyMissingMinIOObject(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, storageKey := seedOneCleanupCandidate(t, ctx, database, now, "missing-object")
	storage := newMinIOTestStorage(t, ctx)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create missing-object cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)
	service := cleanup.NewService(postgresadapter.NewCleanupRepository(pool), storage, cleanupFixedClock{now: now})

	result, err := service.Run(ctx, cleanup.Input{Limit: 10})
	if err != nil {
		t.Fatalf("Run() missing-object replay error = %v", err)
	}
	if result.Selected != 1 || result.Deleted != 1 {
		t.Errorf("Run() missing-object result = %#v, want selected:1 deleted:1", result)
	}
	if _, err := storage.HeadObject(ctx, storageKey); err == nil {
		t.Fatal("missing object unexpectedly exists after cleanup")
	}
	assertCleanupMarker(t, ctx, database, intentID, now)
}

func TestManualCleanupRecoversCrashWindowAfterObjectWasDeletedBeforeMarker(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, storageKey := seedOneCleanupCandidate(t, ctx, database, now, "crash-window")
	storage := newMinIOTestStorage(t, ctx)
	putMinIOObject(t, ctx, storage, storageKey, "image/jpeg", []byte("crash-window object"))
	if err := storage.DeleteObject(ctx, storageKey); err != nil {
		t.Fatalf("simulate physical delete before marker: %v", err)
	}
	var markerBefore *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&markerBefore); err != nil {
		t.Fatalf("read pre-recovery marker: %v", err)
	}
	if markerBefore != nil {
		t.Fatalf("pre-recovery marker = %v, want NULL crash window", markerBefore)
	}

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create crash-window cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)
	service := cleanup.NewService(postgresadapter.NewCleanupRepository(pool), storage, cleanupFixedClock{now: now})
	result, err := service.Run(ctx, cleanup.Input{Limit: 10})
	if err != nil {
		t.Fatalf("Run() crash-window recovery error = %v", err)
	}
	if result.Selected != 1 || result.Deleted != 1 {
		t.Errorf("Run() crash-window result = %#v, want selected:1 deleted:1", result)
	}
	assertCleanupMarker(t, ctx, database, intentID, now)
}

func TestCleanupStorageMapsStoppedMinIOToTransientError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	storage := newStoppedMinIOCleanupStorage(t, ctx)

	err := storage.DeleteObject(ctx, "cleanup/transient")
	if !errors.Is(err, cleanup.ErrTransientCleanup) {
		t.Fatalf("DeleteObject() error = %v, want ErrTransientCleanup", err)
	}
}

func newStoppedMinIOCleanupStorage(
	t *testing.T,
	ctx context.Context,
) *s3storageadapter.Adapter {
	t.Helper()
	container := startDisposableMinIO(t, ctx)
	endpoint, err := container.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("get transient-test MinIO endpoint: %v", err)
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(minioRegion),
		awsconfig.WithRetryMaxAttempts(1),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			minioUsername,
			minioPassword,
			"",
		)),
	)
	if err != nil {
		t.Fatalf("load transient-test AWS configuration: %v", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	bucketName := "lawang-cleanup-transient-" + uuid.NewString()
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucketName)}); err != nil {
		t.Fatalf("create transient-test MinIO bucket: %v", err)
	}
	storage := s3storageadapter.New(s3.NewPresignClient(client), bucketName, client)
	if err := container.Stop(ctx, nil); err != nil {
		t.Fatalf("stop transient-test MinIO: %v", err)
	}
	return storage
}

func TestCleanupRepositorySelectionMatrix(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	type cleanupCase struct {
		name             string
		status           string
		changedAt        time.Time
		expiresAt        time.Time
		deletedAt        *time.Time
		artifactBacked   bool
		wantMaterialized bool
		wantCandidate    bool
	}
	alreadyDeletedAt := now.Add(-time.Hour)
	cases := []cleanupCase{
		{name: "pending recently overdue", status: "pending", changedAt: now.Add(-time.Hour), expiresAt: now.Add(-time.Hour), wantMaterialized: true},
		{name: "superseded stale", status: "superseded", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(time.Hour), wantCandidate: true},
		{name: "expired stale", status: "expired", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(-47 * time.Hour), wantCandidate: true},
		{name: "validation failed stale", status: "validation_failed", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(time.Hour), wantCandidate: true},
		{name: "exact grace boundary", status: "expired", changedAt: now.Add(-24 * time.Hour), expiresAt: now.Add(-47 * time.Hour)},
		{name: "confirmed stale", status: "confirmed", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(-47 * time.Hour)},
		{name: "corrupted artifact backed eligible", status: "expired", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(-47 * time.Hour), artifactBacked: true},
		{name: "already deleted eligible", status: "superseded", changedAt: now.Add(-25 * time.Hour), expiresAt: now.Add(time.Hour), deletedAt: &alreadyDeletedAt},
	}

	expected := make(map[uuid.UUID]bool)
	var pendingIntentID uuid.UUID
	for _, fixture := range cases {
		sessionID := uuid.New()
		intentID := uuid.New()
		storageKey := "verification-sessions/" + sessionID.String() + "/identity_document/" + intentID.String()
		var failureCode *string
		if fixture.status == "validation_failed" {
			code := "identity_number_mismatch"
			failureCode = &code
		}
		var confirmedAt *time.Time
		if fixture.status == "confirmed" {
			confirmedAt = &fixture.changedAt
		}
		if _, err := database.Exec(ctx, `
			INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
			VALUES ($1, sha256(convert_to($2, 'UTF8')), $3)
		`, sessionID, "cleanup-matrix-"+fixture.name, now.Add(time.Hour)); err != nil {
			t.Fatalf("seed %q session: %v", fixture.name, err)
		}
		if _, err := database.Exec(ctx, `
			INSERT INTO upload_intents (
				id, verification_session_id, kind, storage_key, status,
				created_at, latest_status_change_at, expires_at, confirmed_at,
				object_deleted_at, failure_code
			) VALUES ($1, $2, 'identity_document', $3, $4, $5, $6, $7, $8, $9, $10)
		`, intentID, sessionID, storageKey, fixture.status, now.Add(-48*time.Hour), fixture.changedAt, fixture.expiresAt, confirmedAt, fixture.deletedAt, failureCode); err != nil {
			t.Fatalf("seed %q intent: %v", fixture.name, err)
		}
		if fixture.artifactBacked {
			if _, err := database.Exec(ctx, `
				INSERT INTO verification_artifacts (
					id, upload_intent_id, verification_session_id, kind, storage_key,
					content_type, size_bytes, etag, created_at
				) VALUES ($1, $2, $3, 'identity_document', $4, 'image/jpeg', 10, 'cleanup-matrix', $5)
			`, uuid.New(), intentID, sessionID, storageKey, fixture.changedAt); err != nil {
				t.Fatalf("seed %q artifact: %v", fixture.name, err)
			}
		}
		if fixture.wantCandidate {
			expected[intentID] = true
		}
		if fixture.wantMaterialized {
			pendingIntentID = intentID
		}
	}

	repository := postgresadapter.NewCleanupRepository(database)
	materialized, err := repository.MaterializeExpiredUploadIntents(ctx, now)
	if err != nil {
		t.Fatalf("MaterializeExpiredUploadIntents() error = %v", err)
	}
	if materialized != 1 {
		t.Fatalf("materialized rows = %d, want 1", materialized)
	}
	candidates, err := repository.ListCleanupCandidates(ctx, now.Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatalf("ListCleanupCandidates() error = %v", err)
	}
	if len(candidates) != len(expected) {
		t.Fatalf("candidate count = %d, want %d: %#v", len(candidates), len(expected), candidates)
	}
	for _, candidate := range candidates {
		if !expected[candidate.IntentID] {
			t.Errorf("unsafe candidate selected: %s", candidate.IntentID)
		}
	}
	var pendingStatus string
	var pendingChangedAt, pendingExpiresAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, latest_status_change_at, expires_at
		FROM upload_intents WHERE id = $1
	`, pendingIntentID).Scan(&pendingStatus, &pendingChangedAt, &pendingExpiresAt); err != nil {
		t.Fatalf("read materialized pending intent: %v", err)
	}
	if pendingStatus != "expired" || !pendingChangedAt.Equal(pendingExpiresAt) {
		t.Errorf("materialized pending row = status:%q changed:%s expiry:%s", pendingStatus, pendingChangedAt, pendingExpiresAt)
	}
}

func TestCleanupRepositoryConcurrentMarkHasOneWinnerAndOneStaleOutcome(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.New()
	intentID := uuid.New()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
		VALUES ($1, sha256(convert_to('cleanup-concurrent-marker', 'UTF8')), $2)
	`, sessionID, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed concurrent marker session: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status,
			created_at, latest_status_change_at, expires_at
		) VALUES ($1, $2, 'identity_document', $3, 'expired', $4, $4, $4)
	`, intentID, sessionID, "cleanup/concurrent-marker", now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("seed concurrent marker intent: %v", err)
	}

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create concurrent marker pool: %v", err)
	}
	t.Cleanup(pool.Close)
	repository := postgresadapter.NewCleanupRepository(pool)
	results := make(chan bool, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			marked, err := repository.MarkObjectDeleted(ctx, intentID, now)
			results <- marked
			errors <- err
		}()
	}
	workers.Wait()
	close(results)
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatalf("MarkObjectDeleted() error = %v", err)
		}
	}
	winners := 0
	stale := 0
	for marked := range results {
		if marked {
			winners++
		} else {
			stale++
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("concurrent marker outcomes = winners:%d stale:%d, want 1 and 1", winners, stale)
	}
}

// TestManualCleanupDeletesOneSafeObjectAndPersistsItsMarker is the real
// PostgreSQL + MinIO tracer for Issue 010 Checkpoint 3.
//
// It intentionally references the future cleanup repository and application
// service. Keep selection and deletion in production code: do not replace the ACT
// with raw SQL or a direct S3 DeleteObject call merely to make this tracer GREEN.
func TestManualCleanupDeletesOneSafeObjectAndPersistsItsMarker(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	safeSessionID := uuid.MustParse("a556967f-8252-4cd7-a4cd-5c013717ad9d")
	safeIntentID := uuid.MustParse("987551ce-0578-4f74-b3fa-b9ea78c0566c")
	protectedSessionID := uuid.MustParse("7cc105f7-61f9-4c14-aa76-e981e72a5545")
	protectedIntentID := uuid.MustParse("d332da95-4e55-450f-a8a0-4098de30bb17")
	safeKey := "verification-sessions/" + safeSessionID.String() +
		"/identity_document/" + safeIntentID.String()
	protectedKey := "verification-sessions/" + protectedSessionID.String() +
		"/identity_document/" + protectedIntentID.String()

	seedManualCleanupTracer(
		t,
		ctx,
		database.Config().ConnString(),
		now,
		safeSessionID,
		safeIntentID,
		safeKey,
		protectedSessionID,
		protectedIntentID,
		protectedKey,
	)
	storage := newMinIOTestStorage(t, ctx)
	putMinIOObject(t, ctx, storage, safeKey, "image/jpeg", []byte("safe orphan"))
	putMinIOObject(t, ctx, storage, protectedKey, "image/jpeg", []byte("confirmed object"))

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create cleanup PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	service := cleanup.NewService(
		postgresadapter.NewCleanupRepository(pool),
		storage,
		cleanupFixedClock{now: now},
	)

	result, err := service.Run(ctx, cleanup.Input{Limit: 10})
	if err != nil {
		t.Fatalf("manual cleanup Run() error = %v", err)
	}
	if result.Selected != 1 || result.Deleted != 1 {
		t.Errorf("manual cleanup result = %#v, want one selected and deleted", result)
	}

	if _, err := storage.HeadObject(ctx, safeKey); err == nil {
		t.Error("safe orphan still exists in MinIO after cleanup")
	}
	if _, err := storage.HeadObject(ctx, protectedKey); err != nil {
		t.Fatalf("confirmed object was deleted or became unreadable: %v", err)
	}

	var safeDeletedAt *time.Time
	var protectedDeletedAt *time.Time
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT object_deleted_at FROM upload_intents WHERE id = $1),
			(SELECT object_deleted_at FROM upload_intents WHERE id = $2)
	`, safeIntentID, protectedIntentID).Scan(&safeDeletedAt, &protectedDeletedAt); err != nil {
		t.Fatalf("read durable cleanup markers: %v", err)
	}
	if safeDeletedAt == nil || !safeDeletedAt.Equal(now) {
		t.Errorf("safe object_deleted_at = %v, want %s", safeDeletedAt, now)
	}
	if protectedDeletedAt != nil {
		t.Errorf("confirmed object_deleted_at = %v, want NULL", protectedDeletedAt)
	}
}

func seedManualCleanupTracer(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
	now time.Time,
	safeSessionID uuid.UUID,
	safeIntentID uuid.UUID,
	safeKey string,
	protectedSessionID uuid.UUID,
	protectedIntentID uuid.UUID,
	protectedKey string,
) {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create cleanup fixture pool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
		VALUES
			($1, decode(repeat('71', 32), 'hex'), $3),
			($2, decode(repeat('72', 32), 'hex'), $3)
	`, safeSessionID, protectedSessionID, now); err != nil {
		t.Fatalf("seed cleanup verification sessions: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status,
			created_at, latest_status_change_at, expires_at, confirmed_at
		)
		VALUES
			($1, $2, 'identity_document', $6, 'superseded', $5::timestamptz - interval '26 hours', $5::timestamptz - interval '25 hours', $5::timestamptz - interval '25 hours', NULL),
			($3, $4, 'identity_document', $7, 'confirmed', $5::timestamptz - interval '26 hours', $5::timestamptz - interval '25 hours', $5::timestamptz - interval '25 hours', $5::timestamptz - interval '25 hours')
	`, safeIntentID, safeSessionID, protectedIntentID, protectedSessionID, now, safeKey, protectedKey); err != nil {
		t.Fatalf("seed cleanup upload intents: %v", err)
	}
}

func seedOneCleanupCandidate(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	now time.Time,
	caseName string,
) (uuid.UUID, string) {
	t.Helper()
	sessionID := uuid.New()
	intentID := uuid.New()
	storageKey := "verification-sessions/" + sessionID.String() + "/identity_document/" + intentID.String()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
		VALUES ($1, sha256(convert_to($2, 'UTF8')), $3)
	`, sessionID, "cleanup-"+caseName, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed %s cleanup session: %v", caseName, err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status,
			created_at, latest_status_change_at, expires_at
		) VALUES ($1, $2, 'identity_document', $3, 'expired', $4, $4, $4)
	`, intentID, sessionID, storageKey, now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("seed %s cleanup intent: %v", caseName, err)
	}
	return intentID, storageKey
}

func assertCleanupMarker(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	intentID uuid.UUID,
	want time.Time,
) {
	t.Helper()
	var got *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&got); err != nil {
		t.Fatalf("read cleanup marker: %v", err)
	}
	if got == nil || !got.Equal(want) {
		t.Errorf("object_deleted_at = %v, want %s", got, want)
	}
}

type cleanupFixedClock struct {
	now time.Time
}

func (c cleanupFixedClock) Now() time.Time { return c.now }
