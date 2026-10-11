package integration_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	queueadapter "github.com/santosidauruk/lawang/internal/adapter/queue"
	s3storageadapter "github.com/santosidauruk/lawang/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type cleanupTransientOnceStorage struct {
	mu       sync.Mutex
	failed   bool
	calls    int
	delegate cleanup.CleanupStorage
}

type switchableCleanupStorage struct {
	mu       sync.RWMutex
	delegate cleanup.CleanupStorage
}

type cancelDuringDeleteStorage struct{ entered chan struct{} }

func (s cancelDuringDeleteStorage) DeleteObject(ctx context.Context, _ string) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (s *switchableCleanupStorage) DeleteObject(ctx context.Context, key string) error {
	s.mu.RLock()
	delegate := s.delegate
	s.mu.RUnlock()
	return delegate.DeleteObject(ctx, key)
}

func (s *switchableCleanupStorage) Set(delegate cleanup.CleanupStorage) {
	s.mu.Lock()
	s.delegate = delegate
	s.mu.Unlock()
}

func (s *cleanupTransientOnceStorage) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *cleanupTransientOnceStorage) DeleteObject(ctx context.Context, key string) error {
	s.mu.Lock()
	s.calls++
	if !s.failed {
		s.failed = true
		s.mu.Unlock()
		return errors.Join(cleanup.ErrTransientCleanup, errors.New("private-storage-key:"+key))
	}
	s.mu.Unlock()
	return s.delegate.DeleteObject(ctx, key)
}

type cleanupTestProviderService struct{}

func (cleanupTestProviderService) SendToProvider(context.Context, uuid.UUID) error { return nil }

type cleanupTestExpiryService struct{}

func (cleanupTestExpiryService) Sweep(context.Context) (expiry.SweepResult, error) {
	return expiry.SweepResult{}, nil
}

type synchronizedLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestScheduledCleanupRedisMinIORetriesAndMarksObject(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, storageKey := seedOneCleanupCandidate(t, ctx, database, now, "scheduled-recovery")
	storage := newMinIOTestStorage(t, ctx)
	putMinIOObject(t, ctx, storage, storageKey, "image/jpeg", []byte("scheduled orphan"))
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)

	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get Redis URL: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisOpt := asynq.RedisClientOpt{Addr: parsedRedisURL.Host}
	logs := &synchronizedLogBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	countingStorage := &cleanupTransientOnceStorage{delegate: storage}
	service := cleanup.NewService(
		postgresadapter.NewCleanupRepository(pool),
		countingStorage,
		cleanupFixedClock{now: now},
	)
	server := queueadapter.NewAsynqTaskServer(
		queueadapter.RedisConfig{Address: parsedRedisURL.Host}, 2, 3*time.Second,
		cleanupTestProviderService{}, cleanupTestExpiryService{}, service, 10, logger,
	)
	if err := server.Start(); err != nil {
		t.Fatalf("start cleanup worker: %v", err)
	}
	t.Cleanup(server.Shutdown)
	client := asynq.NewClient(redisOpt)
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Enqueue(queueadapter.NewStorageCleanupTask(), asynq.MaxRetry(2)); err != nil {
		t.Fatalf("enqueue cleanup task: %v", err)
	}

	inspector := asynq.NewInspector(redisOpt)
	t.Cleanup(func() { _ = inspector.Close() })
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), `"outcome":"retry"`) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), `"outcome":"retry"`) {
		t.Fatalf("transient cleanup attempt was silently skipped; logs: %s", logs.String())
	}
	for time.Now().Before(deadline) {
		tasks, err := inspector.ListRetryTasks("default")
		if err != nil {
			t.Fatalf("inspect retry queue: %v", err)
		}
		if len(tasks) > 0 {
			if _, err := inspector.RunAllRetryTasks("default"); err != nil {
				t.Fatalf("promote retry task: %v", err)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	for time.Now().Before(deadline) {
		var marker *time.Time
		if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&marker); err != nil {
			t.Fatalf("read cleanup marker: %v", err)
		}
		if marker != nil {
			if !marker.Equal(now) {
				t.Fatalf("marker = %v, want %v", marker, now)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertCleanupMarker(t, ctx, database, intentID, now)
	if _, err := storage.HeadObject(ctx, storageKey); err == nil {
		t.Fatal("scheduled cleanup left orphan object in MinIO")
	}
	logged := logs.String()
	for _, required := range []string{`"task_type":"storage:cleanup"`, `"attempt":1`, `"attempt":2`, `"outcome":"retry"`, `"outcome":"success"`} {
		if !strings.Contains(logged, required) {
			t.Errorf("cleanup task logs lack %s: %s", required, logged)
		}
	}
	if strings.Contains(logged, storageKey) || strings.Contains(logged, "private-storage-key") {
		t.Fatalf("cleanup logs leaked storage identity: %s", logged)
	}
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, `"task_type":"storage:cleanup"`) {
			t.Logf("safe task log sample: %s", line)
			break
		}
	}
	if got := countingStorage.Calls(); got != 2 {
		t.Fatalf("storage delete attempts = %d, want failed attempt and retry", got)
	}
	for range 2 {
		if _, err := client.Enqueue(queueadapter.NewStorageCleanupTask()); err != nil {
			t.Fatalf("enqueue duplicate cleanup task: %v", err)
		}
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && strings.Count(logs.String(), `"outcome":"success"`) < 3 {
		time.Sleep(50 * time.Millisecond)
	}
	if got := strings.Count(logs.String(), `"outcome":"success"`); got < 3 {
		t.Fatalf("duplicate tasks did not finish: success logs=%d", got)
	}
	if got := countingStorage.Calls(); got != 2 {
		t.Fatalf("duplicate tasks repeated deletion: calls=%d", got)
	}

	secondID, secondKey := seedOneCleanupCandidate(t, ctx, database, now, "scheduled-tick")
	putMinIOObject(t, ctx, storage, secondKey, "image/jpeg", []byte("second orphan"))
	protectedSessionID := uuid.New()
	protectedID := uuid.New()
	protectedKey := "verification-sessions/" + protectedSessionID.String() + "/identity_document/" + protectedID.String()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
		VALUES ($1, sha256(convert_to('scheduled-protected', 'UTF8')), $2)
	`, protectedSessionID, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed protected session: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status,
			created_at, latest_status_change_at, expires_at, confirmed_at
		) VALUES ($1, $2, 'identity_document', $3, 'confirmed', $4, $4, $4, $4)
	`, protectedID, protectedSessionID, protectedKey, now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("seed protected intent: %v", err)
	}
	putMinIOObject(t, ctx, storage, protectedKey, "image/jpeg", []byte("protected object"))
	scheduler, err := queueadapter.NewWorkerScheduler(
		queueadapter.RedisConfig{Address: parsedRedisURL.Host}, time.Hour, time.Second, logger,
	)
	if err != nil {
		t.Fatalf("construct cleanup scheduler: %v", err)
	}
	if err := scheduler.Start(); err != nil {
		t.Fatalf("start cleanup scheduler: %v", err)
	}
	t.Cleanup(scheduler.Shutdown)
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var marker *time.Time
		if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, secondID).Scan(&marker); err != nil {
			t.Fatalf("read scheduled marker: %v", err)
		}
		if marker != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertCleanupMarker(t, ctx, database, secondID, now)
	if _, err := storage.HeadObject(ctx, secondKey); err == nil {
		t.Error("scheduler left second orphan in MinIO")
	}
	if _, err := storage.HeadObject(ctx, protectedKey); err != nil {
		t.Fatalf("scheduler touched protected object: %v", err)
	}
	var protectedMarker *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, protectedID).Scan(&protectedMarker); err != nil {
		t.Fatalf("read protected marker: %v", err)
	}
	if protectedMarker != nil {
		t.Fatalf("protected marker = %v, want nil", protectedMarker)
	}
	t.Log("Redis retry and scheduler tick deleted MinIO orphans, persisted markers, and preserved protected object")
}

func TestCleanupRetriesRealMinIOOutageThroughRedis(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, key := seedOneCleanupCandidate(t, ctx, database, now, "real-minio-outage")
	minioContainer := startDisposableMinIO(t, ctx)
	endpoint, err := minioContainer.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("get MinIO endpoint: %v", err)
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(minioRegion),
		awsconfig.WithRetryMaxAttempts(1),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(minioUsername, minioPassword, "")),
	)
	if err != nil {
		t.Fatalf("load AWS configuration: %v", err)
	}
	s3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	bucket := "lawang-cleanup-outage-" + uuid.NewString()
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create MinIO bucket: %v", err)
	}
	storage := s3storageadapter.New(s3.NewPresignClient(s3Client), bucket, s3Client)
	putMinIOObject(t, ctx, storage, key, "image/jpeg", []byte("outage recovery"))
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)
	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get Redis URL: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisOpt := asynq.RedisClientOpt{Addr: parsedRedisURL.Host}
	logs := &synchronizedLogBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	switchableStorage := &switchableCleanupStorage{delegate: storage}
	service := cleanup.NewService(postgresadapter.NewCleanupRepository(pool), switchableStorage, cleanupFixedClock{now: now})
	server := queueadapter.NewAsynqTaskServer(
		queueadapter.RedisConfig{Address: parsedRedisURL.Host}, 1, 3*time.Second,
		cleanupTestProviderService{}, cleanupTestExpiryService{}, service, 10, logger,
	)
	if err := server.Start(); err != nil {
		t.Fatalf("start cleanup server: %v", err)
	}
	t.Cleanup(server.Shutdown)
	client := asynq.NewClient(redisOpt)
	t.Cleanup(func() { _ = client.Close() })
	inspector := asynq.NewInspector(redisOpt)
	t.Cleanup(func() { _ = inspector.Close() })
	if err := minioContainer.Stop(ctx, nil); err != nil {
		t.Fatalf("stop MinIO before task: %v", err)
	}
	if _, err := client.Enqueue(queueadapter.NewStorageCleanupTask(), asynq.MaxRetry(2)); err != nil {
		t.Fatalf("enqueue cleanup during MinIO outage: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(logs.String(), `"outcome":"retry"`) {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(logs.String(), `"outcome":"retry"`) {
		t.Fatalf("MinIO outage did not produce retry: %s", logs.String())
	}
	var markerBefore *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&markerBefore); err != nil {
		t.Fatalf("read marker during outage: %v", err)
	}
	if markerBefore != nil {
		t.Fatalf("outage unexpectedly marked object deleted: %v", markerBefore)
	}
	if err := minioContainer.Start(ctx); err != nil {
		t.Fatalf("restart MinIO: %v", err)
	}
	restartedEndpoint, err := minioContainer.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("get restarted MinIO endpoint: %v", err)
	}
	restartedClient := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(restartedEndpoint)
		options.UsePathStyle = true
	})
	restartedStorage := s3storageadapter.New(s3.NewPresignClient(restartedClient), bucket, restartedClient)
	if _, err := restartedStorage.HeadObject(ctx, key); err != nil {
		t.Fatalf("MinIO object unavailable after restart: %v", err)
	}
	switchableStorage.Set(restartedStorage)
	deadline = time.Now().Add(20 * time.Second)
	promoted := false
	for time.Now().Before(deadline) {
		tasks, err := inspector.ListRetryTasks("default")
		if err != nil {
			t.Fatalf("inspect retry queue: %v", err)
		}
		if len(tasks) > 0 {
			if _, err := inspector.RunAllRetryTasks("default"); err != nil {
				t.Fatalf("promote retry task: %v", err)
			}
			promoted = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		var marker *time.Time
		if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&marker); err != nil {
			t.Fatalf("read recovered marker: %v", err)
		}
		if marker != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var recoveredMarker *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&recoveredMarker); err != nil {
		t.Fatalf("read final marker: %v", err)
	}
	if recoveredMarker == nil || !recoveredMarker.Equal(now) {
		retryTasks, _ := inspector.ListRetryTasks("default")
		t.Fatalf("recovered marker = %v, want %v; promoted=%t retry tasks=%d logs=%s", recoveredMarker, now, promoted, len(retryTasks), logs.String())
	}
	if _, err := restartedStorage.HeadObject(ctx, key); err == nil {
		t.Fatal("recovered cleanup left object in MinIO")
	}
	if strings.Contains(logs.String(), key) || !strings.Contains(logs.String(), `"attempt":2`) {
		t.Fatalf("unsafe or missing retry log: %s", logs.String())
	}
	t.Log("real MinIO outage yielded retry; recovered task deleted object and committed marker")
}

func TestCleanupWorkerAndRedisRestartPreservesPendingTask(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, key := seedOneCleanupCandidate(t, ctx, database, now, "redis-restart")
	storage := newMinIOTestStorage(t, ctx)
	putMinIOObject(t, ctx, storage, key, "image/jpeg", []byte("restart orphan"))
	protectedSessionID := uuid.New()
	protectedID := uuid.New()
	protectedKey := "verification-sessions/" + protectedSessionID.String() + "/identity_document/" + protectedID.String()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
		VALUES ($1, sha256(convert_to('restart-protected', 'UTF8')), $2)
	`, protectedSessionID, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed restart protected session: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id, verification_session_id, kind, storage_key, status,
			created_at, latest_status_change_at, expires_at, confirmed_at
		) VALUES ($1, $2, 'identity_document', $3, 'confirmed', $4, $4, $4, $4)
	`, protectedID, protectedSessionID, protectedKey, now.Add(-25*time.Hour)); err != nil {
		t.Fatalf("seed restart protected intent: %v", err)
	}
	putMinIOObject(t, ctx, storage, protectedKey, "image/jpeg", []byte("restart protected"))
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)
	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get Redis URL: %v", err)
	}
	parsed, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(&synchronizedLogBuffer{}, nil))
	service := cleanup.NewService(postgresadapter.NewCleanupRepository(pool), storage, cleanupFixedClock{now: now})
	newServer := func(address string) *queueadapter.Server {
		return queueadapter.NewAsynqTaskServer(
			queueadapter.RedisConfig{Address: address}, 1, 2*time.Second,
			cleanupTestProviderService{}, cleanupTestExpiryService{}, service, 10, logger,
		)
	}
	first := newServer(parsed.Host)
	if err := first.Start(); err != nil {
		t.Fatalf("start first worker: %v", err)
	}
	first.Shutdown()
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: parsed.Host})
	if _, err := client.Enqueue(queueadapter.NewStorageCleanupTask()); err != nil {
		t.Fatalf("enqueue durable pending cleanup: %v", err)
	}
	_ = client.Close()
	if err := redisContainer.Stop(ctx, nil); err != nil {
		t.Fatalf("stop Redis: %v", err)
	}
	if err := redisContainer.Start(ctx); err != nil {
		t.Fatalf("restart Redis: %v", err)
	}
	restartedURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get restarted Redis URL: %v", err)
	}
	restartedParsed, err := url.Parse(restartedURL)
	if err != nil {
		t.Fatalf("parse restarted Redis URL: %v", err)
	}
	second := newServer(restartedParsed.Host)
	if err := second.Start(); err != nil {
		t.Fatalf("start restarted worker: %v", err)
	}
	t.Cleanup(second.Shutdown)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var marker *time.Time
		if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&marker); err != nil {
			t.Fatalf("read recovered marker: %v", err)
		}
		if marker != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertCleanupMarker(t, ctx, database, intentID, now)
	if _, err := storage.HeadObject(ctx, key); err == nil {
		t.Fatal("restart left orphan in MinIO")
	}
	if _, err := storage.HeadObject(ctx, protectedKey); err != nil {
		t.Fatalf("restart cleanup touched protected object: %v", err)
	}
	var protectedMarker *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, protectedID).Scan(&protectedMarker); err != nil {
		t.Fatalf("read restart protected marker: %v", err)
	}
	if protectedMarker != nil {
		t.Fatalf("restart protected marker = %v, want nil", protectedMarker)
	}
	t.Log("pending cleanup task survived worker and Redis restarts and committed marker")
}

func TestCleanupShutdownDuringActiveDeleteIsBoundedAndRecoverable(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID, key := seedOneCleanupCandidate(t, ctx, database, now, "active-delete")
	storage := newMinIOTestStorage(t, ctx)
	putMinIOObject(t, ctx, storage, key, "image/jpeg", []byte("active delete"))
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create cleanup pool: %v", err)
	}
	t.Cleanup(pool.Close)
	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)
	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get Redis URL: %v", err)
	}
	parsed, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis URL: %v", err)
	}
	redisConfig := queueadapter.RedisConfig{Address: parsed.Host}
	logger := slog.New(slog.NewJSONHandler(&synchronizedLogBuffer{}, nil))
	entered := make(chan struct{}, 1)
	blockingService := cleanup.NewService(
		postgresadapter.NewCleanupRepository(pool),
		cancelDuringDeleteStorage{entered: entered}, cleanupFixedClock{now: now},
	)
	first := queueadapter.NewAsynqTaskServer(
		redisConfig, 1, 200*time.Millisecond,
		cleanupTestProviderService{}, cleanupTestExpiryService{}, blockingService, 10, logger,
	)
	if err := first.Start(); err != nil {
		t.Fatalf("start first cleanup worker: %v", err)
	}
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: parsed.Host})
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.Enqueue(queueadapter.NewStorageCleanupTask()); err != nil {
		t.Fatalf("enqueue active-delete task: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		first.Shutdown()
		t.Fatal("cleanup never entered active DeleteObject")
	}
	started := time.Now()
	first.Shutdown()
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("shutdown exceeded bounded drain: %v", elapsed)
	}
	var before *time.Time
	if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&before); err != nil {
		t.Fatalf("read marker after cancelled delete: %v", err)
	}
	if before != nil {
		t.Fatalf("cancelled delete committed marker: %v", before)
	}
	if _, err := storage.HeadObject(ctx, key); err != nil {
		t.Fatalf("cancelled delete removed object: %v", err)
	}
	recoveryService := cleanup.NewService(postgresadapter.NewCleanupRepository(pool), storage, cleanupFixedClock{now: now})
	second := queueadapter.NewAsynqTaskServer(
		redisConfig, 1, 2*time.Second,
		cleanupTestProviderService{}, cleanupTestExpiryService{}, recoveryService, 10, logger,
	)
	if err := second.Start(); err != nil {
		t.Fatalf("start recovery worker: %v", err)
	}
	t.Cleanup(second.Shutdown)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var marker *time.Time
		if err := database.QueryRow(ctx, `SELECT object_deleted_at FROM upload_intents WHERE id = $1`, intentID).Scan(&marker); err != nil {
			t.Fatalf("read recovery marker: %v", err)
		}
		if marker != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertCleanupMarker(t, ctx, database, intentID, now)
	if _, err := storage.HeadObject(ctx, key); err == nil {
		t.Fatal("recovered worker left object in MinIO")
	}
	t.Log("active delete cancelled within configured shutdown bound; restarted worker committed marker")
}
