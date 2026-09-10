package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/outbox"
)

func TestOutboxRelayWithoutEligibleRowIsIdle(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	assertNoAsynqTasks(t, fixture.redisAddress)

	client := newOutboxRelayClient(t, fixture.redisAddress)
	relay := outbox.NewRelay(
		postgres.NewOutboxStore(fixture.database),
		queue.NewPublisher(client),
		fixedClock{now: fixture.now},
		time.Minute,
	)

	if err := relay.RunOnce(fixture.ctx); err != nil {
		t.Fatalf("idle RunOnce() error = %v, want nil", err)
	}
	assertNoAsynqTasks(t, fixture.redisAddress)
}

func TestRedisUnavailableLeavesOutboxRecoverableAfterLease(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	seedOutboxRelayFixture(t, fixture)

	unavailableClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr:         "127.0.0.1:1",
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = unavailableClient.Close() })

	leaseDuration := time.Minute
	store := postgres.NewOutboxStore(fixture.database)
	failedRelay := outbox.NewRelay(
		store,
		queue.NewPublisher(unavailableClient),
		fixedClock{now: fixture.now},
		leaseDuration,
	)
	if err := failedRelay.RunOnce(fixture.ctx); err == nil {
		t.Fatal("RunOnce() error = nil, want Redis publication failure")
	}

	assertFailedOutboxClaim(t, fixture, fixture.now.Add(leaseDuration), 1)
	assertNoAsynqTasks(t, fixture.redisAddress)

	recoveryClient := newOutboxRelayClient(t, fixture.redisAddress)
	recoveryRelay := outbox.NewRelay(
		postgres.NewOutboxStore(fixture.database),
		queue.NewPublisher(recoveryClient),
		fixedClock{now: fixture.now.Add(leaseDuration)},
		leaseDuration,
	)
	if err := recoveryRelay.RunOnce(fixture.ctx); err != nil {
		t.Fatalf("recovery RunOnce() error = %v", err)
	}

	assertRecoveredPublishedOutbox(t, fixture, 2)
	assertOneStableProviderSubmitTask(t, fixture)
}

func TestExpiredCrashWindowClaimRequeuesAsOneLogicalTask(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	seedOutboxRelayFixture(t, fixture)

	leaseDuration := time.Minute
	firstStore := postgres.NewOutboxStore(fixture.database)
	firstToken := uuid.MustParse("170dcd57-e351-4a26-8c50-d91a92a04f49")
	firstClaim, err := firstStore.ClaimNext(
		fixture.ctx,
		fixture.now,
		firstToken,
		fixture.now.Add(leaseDuration),
	)
	if err != nil {
		t.Fatalf("first ClaimNext() error = %v", err)
	}

	firstClient := newOutboxRelayClient(t, fixture.redisAddress)
	if err := queue.NewPublisher(firstClient).Enqueue(fixture.ctx, firstClaim.Task); err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}
	assertOneStableProviderSubmitTask(t, fixture)

	// Simulate a relay crash after Redis accepted the task but before MarkPublished.
	assertActiveUnpublishedClaim(t, fixture, firstToken, fixture.now.Add(leaseDuration), 1)

	secondStore := postgres.NewOutboxStore(fixture.database)
	secondToken := uuid.MustParse("a2665ea1-88df-45f1-9772-436d42d50437")
	secondClaim, err := secondStore.ClaimNext(
		fixture.ctx,
		fixture.now.Add(leaseDuration),
		secondToken,
		fixture.now.Add(2*leaseDuration),
	)
	if err != nil {
		t.Fatalf("reclaim after expiry error = %v", err)
	}
	if secondClaim.Task != firstClaim.Task || secondClaim.ClaimToken != secondToken {
		t.Fatalf("reclaimed outbox = %#v, want same task with second token", secondClaim)
	}

	if err := firstStore.MarkPublished(fixture.ctx, fixture.outboxID, firstToken); !errors.Is(err, outbox.ErrClaimOwnershipLost) {
		t.Fatalf("stale MarkPublished() error = %v, want ErrClaimOwnershipLost", err)
	}
	assertActiveUnpublishedClaim(t, fixture, secondToken, fixture.now.Add(2*leaseDuration), 2)

	secondClient := newOutboxRelayClient(t, fixture.redisAddress)
	if err := queue.NewPublisher(secondClient).Enqueue(fixture.ctx, secondClaim.Task); err != nil {
		t.Fatalf("duplicate Enqueue() error = %v, want successful publication", err)
	}
	if err := secondStore.MarkPublished(fixture.ctx, fixture.outboxID, secondToken); err != nil {
		t.Fatalf("current-owner MarkPublished() error = %v", err)
	}

	assertRecoveredPublishedOutbox(t, fixture, 2)
	assertOneStableProviderSubmitTask(t, fixture)
}

func TestDuplicateAsynqTaskIDCompletesRelayPublication(t *testing.T) {
	fixture := openOutboxRelayFixture(t)
	seedOutboxRelayFixture(t, fixture)

	leaseDuration := time.Minute
	store := postgres.NewOutboxStore(fixture.database)
	firstToken := uuid.MustParse("9ce4135f-973c-48d4-8808-87ee04823d2f")
	firstClaim, err := store.ClaimNext(
		fixture.ctx,
		fixture.now,
		firstToken,
		fixture.now.Add(leaseDuration),
	)
	if err != nil {
		t.Fatalf("first ClaimNext() error = %v", err)
	}

	firstClient := newOutboxRelayClient(t, fixture.redisAddress)
	if err := queue.NewPublisher(firstClient).Enqueue(fixture.ctx, firstClaim.Task); err != nil {
		t.Fatalf("first Enqueue() error = %v", err)
	}
	assertActiveUnpublishedClaim(t, fixture, firstToken, fixture.now.Add(leaseDuration), 1)

	recoveryClient := newOutboxRelayClient(t, fixture.redisAddress)
	recoveryRelay := outbox.NewRelay(
		postgres.NewOutboxStore(fixture.database),
		queue.NewPublisher(recoveryClient),
		fixedClock{now: fixture.now.Add(leaseDuration)},
		leaseDuration,
	)
	if err := recoveryRelay.RunOnce(fixture.ctx); err != nil {
		t.Fatalf("duplicate recovery RunOnce() error = %v", err)
	}

	assertRecoveredPublishedOutbox(t, fixture, 2)
	assertOneStableProviderSubmitTask(t, fixture)
}

func newOutboxRelayClient(t *testing.T, redisAddress string) *asynq.Client {
	t.Helper()
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddress})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func assertFailedOutboxClaim(
	t *testing.T,
	f outboxRelayFixture,
	wantClaimedUntil time.Time,
	wantAttempts int,
) {
	t.Helper()
	var published bool
	var claimToken *uuid.UUID
	var claimedUntil *time.Time
	var attempts int
	var lastError string
	if err := f.database.QueryRow(f.ctx, `
		SELECT
			published_at IS NOT NULL,
			claim_token,
			claimed_until,
			attempt_count,
			last_error_code
		FROM outbox
		WHERE id = $1
	`, f.outboxID).Scan(&published, &claimToken, &claimedUntil, &attempts, &lastError); err != nil {
		t.Fatalf("inspect failed outbox claim: %v", err)
	}
	if published || claimToken == nil || claimedUntil == nil || attempts != wantAttempts || lastError != "queue_publish_failed" {
		t.Fatalf(
			"failed outbox = published:%t token:%v until:%v attempts:%d error:%q",
			published,
			claimToken,
			claimedUntil,
			attempts,
			lastError,
		)
	}
	if !claimedUntil.Equal(wantClaimedUntil) {
		t.Fatalf("failed claimed_until = %s, want %s", claimedUntil, wantClaimedUntil)
	}
}

func assertActiveUnpublishedClaim(
	t *testing.T,
	f outboxRelayFixture,
	wantToken uuid.UUID,
	wantClaimedUntil time.Time,
	wantAttempts int,
) {
	t.Helper()
	var published bool
	var claimToken *uuid.UUID
	var claimedUntil *time.Time
	var attempts int
	if err := f.database.QueryRow(f.ctx, `
		SELECT published_at IS NOT NULL, claim_token, claimed_until, attempt_count
		FROM outbox
		WHERE id = $1
	`, f.outboxID).Scan(&published, &claimToken, &claimedUntil, &attempts); err != nil {
		t.Fatalf("inspect active outbox claim: %v", err)
	}
	if published || claimToken == nil || claimedUntil == nil || *claimToken != wantToken || attempts != wantAttempts {
		t.Fatalf(
			"active outbox = published:%t token:%v until:%v attempts:%d",
			published,
			claimToken,
			claimedUntil,
			attempts,
		)
	}
	if !claimedUntil.Equal(wantClaimedUntil) {
		t.Fatalf("active claimed_until = %s, want %s", claimedUntil, wantClaimedUntil)
	}
}

func assertRecoveredPublishedOutbox(t *testing.T, f outboxRelayFixture, wantAttempts int) {
	t.Helper()
	var published, claimTokenIsNull, claimedUntilIsNull, lastErrorIsNull bool
	var attempts int
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
		&attempts,
		&lastErrorIsNull,
	); err != nil {
		t.Fatalf("inspect recovered published outbox: %v", err)
	}
	if !published || !claimTokenIsNull || !claimedUntilIsNull || attempts != wantAttempts || !lastErrorIsNull {
		t.Fatalf(
			"recovered outbox = published:%t token-null:%t until-null:%t attempts:%d error-null:%t",
			published,
			claimTokenIsNull,
			claimedUntilIsNull,
			attempts,
			lastErrorIsNull,
		)
	}
}
