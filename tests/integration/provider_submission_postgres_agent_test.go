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
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
)

var (
	errProviderSubmissionNotReady   = errors.New("provider submission is not ready")
	errProviderSubmissionWrongState = errors.New("provider submission state is not eligible")
	errForcedProviderSubmission     = errors.New("forced provider submission failure")
)

func TestProviderSubmissionMissingAcceptedArtifactLeavesNoDurableEffects(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	if _, err := database.Exec(ctx, `
		DELETE FROM verification_artifacts
		WHERE id = $1
	`, fixture.biometricArtifactID); err != nil {
		t.Fatalf("remove accepted Biometric Capture fixture: %v", err)
	}

	_, err := exerciseGuardedProviderSubmission(
		ctx,
		postgresadapter.NewProviderSubmissionTransactions(database),
		fixture.sessionID,
		fixture.outboxID,
		fixture.now,
		false,
	)
	if !errors.Is(err, errProviderSubmissionNotReady) {
		t.Fatalf("guarded submission error = %v, want not-ready", err)
	}
	assertNoProviderSubmissionEffects(t, ctx, database, fixture, "biometric_capture_uploaded")
}

func TestProviderSubmissionWrongAndTerminalStatesLeaveNoDurableEffects(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)

	for index, status := range []string{
		"personal_details_submitted",
		"identity_document_uploaded",
		"verified",
		"rejected",
		"expired",
	} {
		t.Run(status, func(t *testing.T) {
			fixture := uniqueProviderSubmissionFixture(index)
			seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)
			if _, err := database.Exec(ctx, `
				UPDATE verification_sessions SET status = $2 WHERE id = $1
			`, fixture.sessionID, status); err != nil {
				t.Fatalf("set guarded-state fixture to %s: %v", status, err)
			}

			_, err := exerciseGuardedProviderSubmission(
				ctx,
				postgresadapter.NewProviderSubmissionTransactions(database),
				fixture.sessionID,
				fixture.outboxID,
				fixture.now,
				false,
			)
			if !errors.Is(err, errProviderSubmissionWrongState) {
				t.Fatalf("guarded submission error = %v, want wrong-state", err)
			}
			assertNoProviderSubmissionEffects(t, ctx, database, fixture, status)
		})
	}
}

func TestProviderSubmissionFailureAfterEventRollsBackStateDeadlineAndEvent(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	_, err := exerciseGuardedProviderSubmission(
		ctx,
		postgresadapter.NewProviderSubmissionTransactions(database),
		fixture.sessionID,
		fixture.outboxID,
		fixture.now,
		true,
	)
	if !errors.Is(err, errForcedProviderSubmission) {
		t.Fatalf("guarded submission error = %v, want forced failure", err)
	}
	assertNoProviderSubmissionEffects(t, ctx, database, fixture, "biometric_capture_uploaded")
}

func TestConcurrentFirstProviderSubmissionsConvergeToOneDurableOutcome(t *testing.T) {
	ctx, database := openProviderSubmissionDatabase(t)
	fixture := newProviderSubmissionSuccessFixture()
	seedProviderSubmissionSuccessFixture(t, ctx, database, fixture)

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create Provider Submission PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	transactions := postgresadapter.NewProviderSubmissionTransactions(pool)
	start := make(chan struct{})
	type result struct {
		replayed bool
		err      error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, outboxID := range []uuid.UUID{fixture.outboxID, uuid.New()} {
		go func(outboxID uuid.UUID) {
			ready.Done()
			<-start
			replayed, err := exerciseGuardedProviderSubmission(
				ctx,
				transactions,
				fixture.sessionID,
				outboxID,
				fixture.now,
				false,
			)
			results <- result{replayed: replayed, err: err}
		}(outboxID)
	}
	ready.Wait()
	close(start)

	replayedCount := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent guarded submission error = %v", result.err)
		}
		if result.replayed {
			replayedCount++
		}
	}
	if replayedCount != 1 {
		t.Fatalf("durable replay results = %d, want exactly 1", replayedCount)
	}

	var status string
	var deadline time.Time
	var submitEvents, outboxRows int
	if err := database.QueryRow(ctx, `
		SELECT status, verification_deadline_at,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'submit_session'),
			(SELECT count(*) FROM outbox WHERE verification_session_id = $1 AND task_type = 'provider:submit')
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&status, &deadline, &submitEvents, &outboxRows); err != nil {
		t.Fatalf("read concurrent Provider Submission outcome: %v", err)
	}
	if status != "verification_pending" ||
		!deadline.Equal(fixture.now.Add(24*time.Hour)) ||
		submitEvents != 1 ||
		outboxRows != 1 {
		t.Errorf(
			"concurrent outcome = status:%q deadline:%s events:%d outbox:%d",
			status,
			deadline,
			submitEvents,
			outboxRows,
		)
	}
}

// exerciseGuardedProviderSubmission probes the Checkpoint 1 PostgreSQL operation.
// The public application service remains user-owned in Checkpoint 3.
func exerciseGuardedProviderSubmission(
	ctx context.Context,
	transactions providersubmission.Transactor,
	sessionID uuid.UUID,
	outboxID uuid.UUID,
	submittedAt time.Time,
	failAfterEvent bool,
) (bool, error) {
	replayed := false
	err := transactions.WithinTransaction(ctx, func(tx providersubmission.Transaction) error {
		stored, err := tx.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if stored.Status == session.StatusVerificationPending {
			replayed = true
			return nil
		}
		if stored.Status != session.StatusBiometricCaptureUploaded {
			return errProviderSubmissionWrongState
		}

		ready, err := tx.HasRequiredAcceptedArtifacts(ctx, sessionID)
		if err != nil {
			return err
		}
		if !ready {
			return errProviderSubmissionNotReady
		}
		if err := tx.SetPendingVerification(ctx, submittedAt, sessionID); err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID:  sessionID,
			Type:       sessionevent.SubmitSession,
			Metadata:   sessionevent.EmptyMetadata(),
			OccurredAt: submittedAt,
		}); err != nil {
			return err
		}
		if failAfterEvent {
			return errForcedProviderSubmission
		}
		return tx.InsertUnpublishedOutbox(ctx, outboxID, sessionID)
	})
	return replayed, err
}

func assertNoProviderSubmissionEffects(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	fixture providerSubmissionSuccessFixture,
	wantStatus string,
) {
	t.Helper()
	var status string
	var deadlineIsNull bool
	var submitEvents, outboxRows int
	if err := database.QueryRow(ctx, `
		SELECT status, verification_deadline_at IS NULL,
			(SELECT count(*) FROM session_events WHERE session_id = $1 AND event_type = 'submit_session'),
			(SELECT count(*) FROM outbox WHERE verification_session_id = $1 AND task_type = 'provider:submit')
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(&status, &deadlineIsNull, &submitEvents, &outboxRows); err != nil {
		t.Fatalf("read rejected Provider Submission effects: %v", err)
	}
	if status != wantStatus || !deadlineIsNull || submitEvents != 0 || outboxRows != 0 {
		t.Errorf(
			"rejected outcome = status:%q deadline-null:%t events:%d outbox:%d",
			status,
			deadlineIsNull,
			submitEvents,
			outboxRows,
		)
	}
}

func uniqueProviderSubmissionFixture(index int) providerSubmissionSuccessFixture {
	fixture := newProviderSubmissionSuccessFixture()
	fixture.now = fixture.now.Add(time.Duration(index) * time.Hour)
	fixture.sessionID = uuid.New()
	fixture.rawToken = "provider-submission-agent-" + fixture.sessionID.String()
	fixture.identityIntentID = uuid.New()
	fixture.identityArtifactID = uuid.New()
	fixture.biometricIntentID = uuid.New()
	fixture.biometricArtifactID = uuid.New()
	fixture.outboxID = uuid.New()
	return fixture
}
