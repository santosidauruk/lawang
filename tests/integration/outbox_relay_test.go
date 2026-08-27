package integration_test

import (
	"context"
	"encoding/json"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/adapter/queue"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

const outboxRelayRedisImage = "redis:7.4-alpine"

// TestFirstOutboxRelayPublishesOneStableTask is the agent-scaffolded RED tracer
// for Issue 009 Checkpoint 4.
//
// Keep this as one behavior: one eligible unpublished provider:submit outbox row
// is claimed in a short PostgreSQL transaction, handed to real Redis/Asynq after
// that transaction closes, and marked published with the same claim ownership.
// Crash, retry, duplicate, expired-lease, and concurrent-relay cases are outside
// this tracer. The user-owned concurrent proof is the next learning slice.
func TestFirstOutboxRelayPublishesOneStableTask(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	seedOutboxRelayFixture(t, fixture)
	assertOutboxAwaitingFirstClaim(t, fixture)
	assertNoAsynqTasks(t, fixture.redisAddress)

	// ACT — user-owned production wiring starts here. Construct the PostgreSQL
	// outbox adapter, application Relay, and Asynq queue adapter, then invoke
	// Relay.RunOnce exactly once. Keep Redis I/O outside the claim transaction.
	runFirstOutboxRelay(t, fixture)

	// ASSERT — durable publication means queue handoff succeeded. It does not mean
	// the provider task ran or a provider verdict was applied.
	assertPublishedOutbox(t, fixture)
	assertOneStableProviderSubmitTask(t, fixture)
}

type outboxRelayFixture struct {
	ctx          context.Context
	database     *pgxpool.Pool
	redisAddress string
	now          time.Time
	sessionID    uuid.UUID
	outboxID     uuid.UUID
}

func openOutboxRelayFixture(t *testing.T) outboxRelayFixture {
	t.Helper()
	ctx, postgresContainer, databaseURL := openUploadIntentDatabase(t)
	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
	} {
		runPSQLFile(t, ctx, postgresContainer, migration.hostPath, migration.containerPath)
	}

	database, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create outbox relay PostgreSQL pool: %v", err)
	}
	t.Cleanup(database.Close)

	redisContainer, err := tcredis.Run(ctx, outboxRelayRedisImage)
	if err != nil {
		t.Fatalf("start disposable Redis: %v", err)
	}
	testcontainers.CleanupContainer(t, redisContainer)

	redisURL, err := redisContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Redis connection string: %v", err)
	}
	parsedRedisURL, err := url.Parse(redisURL)
	if err != nil {
		t.Fatalf("parse Redis connection string: %v", err)
	}

	return outboxRelayFixture{
		ctx:          ctx,
		database:     database,
		redisAddress: parsedRedisURL.Host,
		now:          time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC),
		sessionID:    uuid.MustParse("e11fe612-032f-4ffc-8d72-cd1751fa03c8"),
		outboxID:     uuid.MustParse("d0e46c5b-ad35-49dd-a95a-d245f38549c7"),
	}
}

func seedOutboxRelayFixture(t *testing.T, f outboxRelayFixture) {
	t.Helper()
	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO verification_sessions (
			id,
			resume_token_hash,
			status,
			created_at,
			updated_at,
			expires_at,
			verification_deadline_at
		)
		VALUES ($1, decode(repeat('42', 32), 'hex'), 'verification_pending', $2, $2, $3, $4)
	`, f.sessionID, f.now.Add(-time.Minute), f.now.Add(30*time.Minute), f.now.Add(24*time.Hour)); err != nil {
		t.Fatalf("insert pending Verification Session: %v", err)
	}

	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_session', $2)
	`, f.sessionID, f.now.Add(-time.Minute)); err != nil {
		t.Fatalf("insert submit_session history: %v", err)
	}

	if _, err := f.database.Exec(f.ctx, `
		INSERT INTO outbox (id, verification_session_id, task_type, payload, created_at)
		VALUES (
			$1,
			$2,
			'provider:submit',
			jsonb_build_object('sessionId', $2::uuid),
			$3
		)
	`, f.outboxID, f.sessionID, f.now); err != nil {
		t.Fatalf("insert unpublished provider submission outbox: %v", err)
	}
}

func runFirstOutboxRelay(t *testing.T, f outboxRelayFixture) {
	t.Helper()

	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr: f.redisAddress,
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	store := postgres.NewOutboxStore(f.database)
	publisher := queue.NewPublisher(client)
	leaseDuration := 1 * time.Minute

	relay := outbox.NewRelay(store, publisher, fixedClock{now: f.now}, leaseDuration)
	err := relay.RunOnce(f.ctx)
	if err != nil {
		t.Fatalf("run first outbox relay: %v", err)
	}
}

func assertOutboxAwaitingFirstClaim(t *testing.T, f outboxRelayFixture) {
	t.Helper()
	var unpublished, claimTokenIsNull, claimedUntilIsNull, lastErrorIsNull bool
	var attemptCount int
	if err := f.database.QueryRow(f.ctx, `
		SELECT
			published_at IS NULL,
			claim_token IS NULL,
			claimed_until IS NULL,
			attempt_count,
			last_error_code IS NULL
		FROM outbox
		WHERE id = $1
	`, f.outboxID).Scan(
		&unpublished,
		&claimTokenIsNull,
		&claimedUntilIsNull,
		&attemptCount,
		&lastErrorIsNull,
	); err != nil {
		t.Fatalf("inspect initial outbox row: %v", err)
	}
	if !unpublished || !claimTokenIsNull || !claimedUntilIsNull || attemptCount != 0 || !lastErrorIsNull {
		t.Fatalf(
			"initial outbox = unpublished:%t claim-token-null:%t claimed-until-null:%t attempts:%d last-error-null:%t",
			unpublished,
			claimTokenIsNull,
			claimedUntilIsNull,
			attemptCount,
			lastErrorIsNull,
		)
	}
}

func assertPublishedOutbox(t *testing.T, f outboxRelayFixture) {
	t.Helper()
	var published, claimTokenIsNull, claimedUntilIsNull, lastErrorIsNull bool
	var attemptCount int
	if err := f.database.QueryRow(f.ctx, `
		SELECT
			published_at IS NOT NULL,
			claim_token IS NULL,
			claimed_until IS NULL,
			attempt_count,
			last_error_code IS NULL
		FROM outbox
		WHERE id = $1
	`, f.outboxID).Scan(
		&published,
		&claimTokenIsNull,
		&claimedUntilIsNull,
		&attemptCount,
		&lastErrorIsNull,
	); err != nil {
		t.Fatalf("inspect published outbox row: %v", err)
	}
	if !published || !claimTokenIsNull || !claimedUntilIsNull || attemptCount != 1 || !lastErrorIsNull {
		t.Errorf(
			"published outbox = published:%t claim-token-null:%t claimed-until-null:%t attempts:%d last-error-null:%t",
			published,
			claimTokenIsNull,
			claimedUntilIsNull,
			attemptCount,
			lastErrorIsNull,
		)
	}
}

func assertNoAsynqTasks(t *testing.T, redisAddress string) {
	t.Helper()
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: redisAddress})
	t.Cleanup(func() { _ = inspector.Close() })
	queues, err := inspector.Queues()
	if err != nil {
		t.Fatalf("inspect initial Asynq queues: %v", err)
	}
	if len(queues) != 0 {
		t.Fatalf("initial Asynq queues = %v, want none", queues)
	}
}

func assertOneStableProviderSubmitTask(t *testing.T, f outboxRelayFixture) {
	t.Helper()
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: f.redisAddress})
	t.Cleanup(func() { _ = inspector.Close() })

	queues, err := inspector.Queues()
	if err != nil {
		t.Fatalf("inspect Asynq queues: %v", err)
	}
	var tasks []*asynq.TaskInfo
	for _, queue := range queues {
		pending, err := inspector.ListPendingTasks(queue)
		if err != nil {
			t.Fatalf("list pending Asynq tasks in %q: %v", queue, err)
		}
		tasks = append(tasks, pending...)
	}
	if len(tasks) != 1 {
		t.Fatalf("pending Asynq tasks = %d across queues %v, want exactly one", len(tasks), queues)
	}

	task := tasks[0]
	var payload struct {
		SessionID uuid.UUID `json:"sessionId"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		t.Fatalf("decode provider:submit task payload: %v", err)
	}
	if task.ID != f.outboxID.String() || task.Type != "provider:submit" || payload.SessionID != f.sessionID {
		t.Errorf(
			"Asynq task = id:%q type:%q session:%s, want id:%q type:%q session:%s",
			task.ID,
			task.Type,
			payload.SessionID,
			f.outboxID,
			"provider:submit",
			f.sessionID,
		)
	}
}

func TestConcurrentRelayRecordedSuccessAllowsOnlyOneWinner(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	leaseDuration := 1 * time.Minute

	seedOutboxRelayFixture(t, fixture)
	assertOutboxAwaitingFirstClaim(t, fixture)
	assertNoAsynqTasks(t, fixture.redisAddress)

	control, err := fixture.database.Acquire(fixture.ctx)
	if err != nil {
		t.Fatalf("connect control database: %v", err)
	}
	t.Cleanup(control.Release)

	testCtx, cancel := context.WithTimeout(fixture.ctx, 10*time.Second)
	t.Cleanup(cancel)

	competitionConfig, err := pgxpool.ParseConfig(fixture.database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse competing PostgreSQL pool config: %v", err)
	}

	competitionConfig.MaxConns = 2
	competitionPool, err := pgxpool.NewWithConfig(testCtx, competitionConfig)
	if err != nil {
		t.Fatalf("create competing PostgreSQL pool: %v", err)
	}
	t.Cleanup(competitionPool.Close)

	connectionA, err := competitionPool.Acquire(testCtx)
	if err != nil {
		t.Fatalf("acquire relay A PostgreSQL connection: %v", err)
	}
	t.Cleanup(connectionA.Release)

	connectionB, err := competitionPool.Acquire(testCtx)
	if err != nil {
		t.Fatalf("acquire relay B PostgreSQL connection: %v", err)
	}
	t.Cleanup(connectionB.Release)

	storeA := postgres.NewOutboxStore(connectionA)
	storeB := postgres.NewOutboxStore(connectionB)

	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr: fixture.redisAddress,
	})
	t.Cleanup(func() {
		_ = client.Close()
	})

	realPublisher := queue.NewPublisher(client)
	blockingPublisher := newBlockingQueuePublisher(realPublisher)
	t.Cleanup(blockingPublisher.release)

	relayA := outbox.NewRelay(
		storeA,
		blockingPublisher,
		fixedClock{now: fixture.now},
		leaseDuration,
	)
	relayB := outbox.NewRelay(
		storeB,
		blockingPublisher,
		fixedClock{now: fixture.now},
		leaseDuration,
	)

	type relayResult struct {
		name string
		err  error
	}

	actors := []struct {
		name  string
		relay *outbox.Relay
	}{
		{name: "relay-a", relay: relayA},
		{name: "relay-b", relay: relayB},
	}

	start := make(chan struct{})
	results := make(chan relayResult, len(actors))

	var ready, actorsDone sync.WaitGroup
	ready.Add(len(actors))
	actorsDone.Add(len(actors))

	for _, actor := range actors {
		go func() {
			defer actorsDone.Done()
			ready.Done()

			select {
			case <-start:
			case <-testCtx.Done():
				results <- relayResult{
					name: actor.name,
					err:  testCtx.Err(),
				}
				return
			}

			results <- relayResult{
				name: actor.name,
				err:  actor.relay.RunOnce(testCtx),
			}
		}()
	}

	t.Cleanup(func() {
		cancel()
		blockingPublisher.release()
		actorsDone.Wait()
	})

	ready.Wait()
	close(start)

	// Berikutnya:
	//
	// 1. Tunggu blockingPublisher memberi sinyal bahwa winner masuk Enqueue.
	select {
	case <-blockingPublisher.entered:
	case <-testCtx.Done():
		t.Fatalf("no relay reached queue publisher: %v", testCtx.Err())
	}
	// 2. Selagi winner tertahan, ambil satu result milik loser.
	var loser relayResult
	select {
	case loser = <-results:
	case <-testCtx.Done():
		t.Fatalf("losing relay did not finish while winner was blocked: %v", testCtx.Err())
	}
	// 3. Assert loser treats no eligible row as an idle run.
	if loser.err != nil {
		t.Fatalf("%s idle result error = %v, want nil", loser.name, loser.err)
	}

	// 4. Gunakan fixture.database untuk membaca exact active claim.
	if got := blockingPublisher.callCount(); got != 1 {
		t.Fatalf(
			"publisher should only called by the winner 1 time, got %d",
			got,
		)
	}

	var activeClaimToken *uuid.UUID
	var activeClaimedUntil *time.Time
	var activeIsPublished bool
	var activeAttemptCount int
	if err := fixture.database.QueryRow(fixture.ctx, `
  	  select claim_token, claimed_until, published_at is not null as is_published, attempt_count
  		from outbox
  		where id = $1
  	`, fixture.outboxID).Scan(
		&activeClaimToken,
		&activeClaimedUntil,
		&activeIsPublished,
		&activeAttemptCount,
	); err != nil {
		t.Fatalf("active claim outbox: %v", err)
	}

	if activeClaimToken == nil {
		t.Fatal("active claim: claim_token is NULL, want non-NULL")
	}

	if activeClaimedUntil == nil {
		t.Fatal("active claim: claimed_until is NULL, want non-NULL")
	}

	expectedClaimedUntil := fixture.now.Add(leaseDuration)
	if got, want := *activeClaimedUntil, expectedClaimedUntil; !got.Equal(want) {
		t.Fatalf("active claim: claimed_until got %v, want %v", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	if activeIsPublished {
		t.Fatal("active claim: published_at is not NULL, want NULL")
	}

	if activeAttemptCount != 1 {
		t.Fatalf(
			"active claim: attempt_count got %d, want 1",
			activeAttemptCount,
		)
	}

	if blockingPublisher.callCount() != 1 {
		t.Errorf("blockingPublisher calls got %d, want 1", blockingPublisher.callCount())
	}

	// 5. Coba MarkPublished dengan non-owner token yang berbeda dari active claim token melalui adapter terpisah.
	differentClaimToken := uuid.New()
	storeControl := postgres.NewOutboxStore(control)
	err = storeControl.MarkPublished(testCtx, fixture.outboxID, differentClaimToken)
	if err == nil {
		t.Fatal("MarkPublished should return err")
	}

	if *activeClaimToken == differentClaimToken {
		t.Fatalf("active claim token is the same with different claim token")
	}

	var nonOwnerClaimToken *uuid.UUID
	var nonOwnerClaimedUntil *time.Time
	var nonOwnerIsPublished bool
	var nonOwnerAttemptCount int
	if err := fixture.database.QueryRow(fixture.ctx, `
  	  select claim_token, claimed_until, published_at is not null as is_published, attempt_count
  		from outbox
  		where id = $1
  	`, fixture.outboxID).Scan(
		&nonOwnerClaimToken,
		&nonOwnerClaimedUntil,
		&nonOwnerIsPublished,
		&nonOwnerAttemptCount,
	); err != nil {
		t.Fatalf("active claim outbox: %v", err)
	}

	if nonOwnerIsPublished {
		t.Fatal("published_at is non-NULL, want NULL")
	}

	if nonOwnerClaimToken == nil {
		t.Fatal("non-owner claim: claim_token is NULL, want non-NULL")
	}

	if nonOwnerClaimedUntil == nil {
		t.Fatal("non-owner claim: claimed_until is NULL, want non-NULL")
	}

	if *activeClaimToken != *nonOwnerClaimToken {
		t.Fatalf("persisted token changed from active claim token")
	}

	if a, r := *activeClaimedUntil, *nonOwnerClaimedUntil; !a.Equal(r) {
		t.Fatalf("persisted claimed_until changed after non-owner mark")
	}

	if nonOwnerAttemptCount != 1 {
		t.Fatalf("attempt count got %d, want 1", nonOwnerAttemptCount)
	}

	// 6. Pastikan stale mark ditolak dan row belum published.
	// 7. Release blockingPublisher.
	blockingPublisher.release()

	// 8. Ambil result winner dan require nil.
	var winner relayResult
	select {
	case winner = <-results:
	case <-testCtx.Done():
		t.Fatalf("winner relay did not finish: %v", testCtx.Err())
	}

	if winner.err != nil {
		t.Fatalf("%s winner RunOnce: %v", winner.name, winner.err)
	}

	// 9. Jalankan final PostgreSQL dan Redis assertions.
	assertPublishedOutbox(t, fixture)
	assertOneStableProviderSubmitTask(t, fixture)

	if blockingPublisher.callCount() != 1 {
		t.Errorf("blockingPublisher calls got %d, want 1", blockingPublisher.callCount())
	}
}

type blockingQueuePublisher struct {
	delegate    *queue.Publisher
	entered     chan struct{}
	releaseGate chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	calls       int
}

func newBlockingQueuePublisher(realPublisher *queue.Publisher) *blockingQueuePublisher {
	return &blockingQueuePublisher{
		delegate:    realPublisher,
		entered:     make(chan struct{}),
		releaseGate: make(chan struct{}),
	}
}

func (p *blockingQueuePublisher) Enqueue(ctx context.Context, task outbox.Task) error {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	p.enterOnce.Do(func() {
		close(p.entered)
	})

	select {
	case <-p.releaseGate:
	case <-ctx.Done():
		return ctx.Err()
	}

	return p.delegate.Enqueue(ctx, task)
}

func (o *blockingQueuePublisher) release() {
	o.releaseOnce.Do(func() { close(o.releaseGate) })
}

func (o *blockingQueuePublisher) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}
