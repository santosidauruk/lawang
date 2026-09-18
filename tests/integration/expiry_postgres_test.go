package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
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

func TestPostgresExpiryRollsBackGuardedUpdateWhenEventAppendFails(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("16a13c90-e517-4f1f-aa61-31f6c0951361")
	seedCreatedSessionDueForExpiry(t, ctx, database, sessionID, now)

	transactions := postgresadapter.NewExpiryTransactions(database)
	err := transactions.WithinTransaction(ctx, func(tx expiry.Transaction) error {
		stored, err := tx.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}
		changed, err := tx.GuardExpiry(ctx, sessionID, stored.Status, now)
		if err != nil {
			return err
		}
		if !changed {
			return session.ErrSessionTransitionStale
		}
		return tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID:  sessionID,
			Type:       sessionevent.Type("invalid_expire"),
			Metadata:   sessionevent.EmptyMetadata(),
			OccurredAt: now,
		})
	})
	if err == nil {
		t.Fatal("WithinTransaction() error = nil, want forced event failure")
	}
	if errors.Is(err, session.ErrSessionTransitionStale) {
		t.Fatalf("WithinTransaction() error = %v, event failure was not reached", err)
	}

	var status string
	var updatedAt time.Time
	var expiryEvents int
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			updated_at,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'expire')
		FROM verification_sessions
		WHERE id = $1
	`, sessionID).Scan(&status, &updatedAt, &expiryEvents); err != nil {
		t.Fatalf("read rolled-back expiry outcome: %v", err)
	}
	wantUpdatedAt := now.Add(-30 * time.Minute)
	if status != verificationsession.Created.String() ||
		!updatedAt.Equal(wantUpdatedAt) ||
		expiryEvents != 0 {
		t.Errorf(
			"rolled-back expiry = status:%q updated_at:%s events:%d, want created at %s with no event",
			status,
			updatedAt,
			expiryEvents,
			wantUpdatedAt,
		)
	}
}

func TestPostgresExpiryStaleGuardLeavesSessionAndEventsUnchanged(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("f07a6502-b310-452b-b30f-367ff0efab91")
	seedCreatedSessionDueForExpiry(t, ctx, database, sessionID, now)

	transactions := postgresadapter.NewExpiryTransactions(database)
	err := transactions.WithinTransaction(ctx, func(tx expiry.Transaction) error {
		if _, err := tx.LockSession(ctx, sessionID); err != nil {
			return err
		}
		changed, err := tx.GuardExpiry(
			ctx,
			sessionID,
			verificationsession.PersonalDetailsSubmitted,
			now,
		)
		if err != nil {
			return err
		}
		if changed {
			return errors.New("stale expiry guard unexpectedly changed a row")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTransaction() stale guard error = %v", err)
	}

	var status string
	var updatedAt time.Time
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			updated_at,
			(SELECT count(*) FROM session_events WHERE session_id = $1)
		FROM verification_sessions
		WHERE id = $1
	`, sessionID).Scan(&status, &updatedAt, &eventCount); err != nil {
		t.Fatalf("read stale expiry outcome: %v", err)
	}
	wantUpdatedAt := now.Add(-30 * time.Minute)
	if status != verificationsession.Created.String() ||
		!updatedAt.Equal(wantUpdatedAt) ||
		eventCount != 0 {
		t.Errorf(
			"stale expiry = status:%q updated_at:%s events:%d, want created at %s with no event",
			status,
			updatedAt,
			eventCount,
			wantUpdatedAt,
		)
	}
}

func TestPostgresPendingExpiryWithoutDeadlineFailsWithoutWrites(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	now := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("20db9b77-bd3b-4693-9255-d3b01b2a2706")
	updatedAt := now.Add(-time.Hour)
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id, status, resume_token_hash, expires_at, created_at, updated_at,
			verification_deadline_at
		)
		VALUES (
			$1, 'verification_pending', decode(repeat('62', 32), 'hex'),
			$2, $3, $3, NULL
		)
	`, sessionID, now.Add(-30*time.Minute), updatedAt); err != nil {
		t.Fatalf("insert pending Verification Session without deadline: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_session', $2)
	`, sessionID, updatedAt); err != nil {
		t.Fatalf("insert submit_session history: %v", err)
	}

	service := expiry.NewService(
		postgresadapter.NewExpiryTransactions(database),
		fixedClock{now: now},
	)
	got, err := service.Expire(ctx, expiry.Input{SessionID: sessionID})
	if err == nil {
		t.Fatal("Expire() error = nil, want missing provider deadline failure")
	}
	if got != (expiry.Result{}) {
		t.Errorf("Expire() result = %#v, want empty result", got)
	}

	var status string
	var storedUpdatedAt time.Time
	var deadlineIsNull bool
	var expiryEvents, totalEvents int
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			updated_at,
			verification_deadline_at IS NULL,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'expire'),
			(SELECT count(*) FROM session_events WHERE session_id = $1)
		FROM verification_sessions
		WHERE id = $1
	`, sessionID).Scan(
		&status,
		&storedUpdatedAt,
		&deadlineIsNull,
		&expiryEvents,
		&totalEvents,
	); err != nil {
		t.Fatalf("read missing-deadline expiry outcome: %v", err)
	}
	if status != verificationsession.VerificationPending.String() ||
		!storedUpdatedAt.Equal(updatedAt) ||
		!deadlineIsNull ||
		expiryEvents != 0 ||
		totalEvents != 1 {
		t.Errorf(
			"missing-deadline outcome = status:%q updated:%s deadline-null:%t expire-events:%d total-events:%d",
			status,
			storedUpdatedAt,
			deadlineIsNull,
			expiryEvents,
			totalEvents,
		)
	}
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
