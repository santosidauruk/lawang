package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

// TestConcurrentGuardedExpiryCommitsOneStateAndOneEvent is the disposable
// PostgreSQL concurrency tracer for Issue 010 Checkpoint 1.
//
// Keep this as one behavior: two callers race through the same production service
// and PostgreSQL transaction adapter, but only one caller reports a state change
// and only one expire event is committed. HTTP, Asynq, scheduled scans, rollback,
// terminal states, and repeated sequential calls remain outside this user tracer.
func TestConcurrentGuardedExpiryCommitsOneStateAndOneEvent(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("e4ab4956-f8b5-4b3c-a409-b2b52daec9c3")
	seedCreatedSessionDueForExpiry(t, ctx, database, sessionID, now)

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create expiry PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	service := expiry.NewService(
		postgresadapter.NewExpiryTransactions(pool),
		fixedClock{now: now},
	)
	start := make(chan struct{})
	type callResult struct {
		result expiry.Result
		err    error
	}
	results := make(chan callResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			result, err := service.Expire(ctx, expiry.Input{SessionID: sessionID})
			results <- callResult{result: result, err: err}
		}()
	}
	ready.Wait()
	close(start)

	changed := 0
	for range 2 {
		call := <-results
		if call.err != nil {
			t.Fatalf("concurrent Expire() error = %v", call.err)
		}
		if call.result.ID != sessionID || call.result.Status != verificationsession.Expired {
			t.Errorf("concurrent Expire() result = %#v, want expired session %s", call.result, sessionID)
		}
		if call.result.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("changed expiry results = %d, want exactly 1", changed)
	}

	assertOneCommittedExpiry(t, ctx, database, sessionID, now)
}

func seedCreatedSessionDueForExpiry(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	now time.Time,
) {
	t.Helper()
	createdAt := now.Add(-30 * time.Minute)
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id,
			status,
			resume_token_hash,
			expires_at,
			created_at,
			updated_at
		)
		VALUES ($1, 'created', decode(repeat('61', 32), 'hex'), $2, $3, $3)
	`, sessionID, now, createdAt); err != nil {
		t.Fatalf("insert due created Verification Session: %v", err)
	}
}

func assertOneCommittedExpiry(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	sessionID uuid.UUID,
	now time.Time,
) {
	t.Helper()
	var status string
	var updatedAt time.Time
	var expiryEvents int
	var exactEvent bool
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			updated_at,
			(
				SELECT count(*)
				FROM session_events
				WHERE session_id = $1 AND event_type = 'expire'
			),
			(
				SELECT COALESCE(bool_and(metadata = '{}'::jsonb AND occurred_at = $2), false)
				FROM session_events
				WHERE session_id = $1 AND event_type = 'expire'
			)
		FROM verification_sessions
		WHERE id = $1
	`, sessionID, now).Scan(&status, &updatedAt, &expiryEvents, &exactEvent); err != nil {
		t.Fatalf("read committed expiry outcome: %v", err)
	}
	if status != session.StatusExpired.String() ||
		!updatedAt.Equal(now) ||
		expiryEvents != 1 ||
		!exactEvent {
		t.Errorf(
			"committed expiry = status:%q updated_at:%s events:%d exact-event:%t, want expired at %s with one safe event",
			status,
			updatedAt,
			expiryEvents,
			exactEvent,
			now,
		)
	}
}
