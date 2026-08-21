package integration_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

// TestConcurrentBiometricPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce
// is the Issue 008 Checkpoint 6 user-owned tracer.
//
// Keep this first cycle to one public behavior: two concurrent Confirm calls for the
// same pending Biometric Capture Upload Intent both return the same recorded success,
// while HeadObject runs once, Extract never runs, and PostgreSQL stores one complete
// accepted outcome.
func TestConcurrentBiometricPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce(t *testing.T) {
	// ARRANGE 1 — disposable PostgreSQL with at least two independent connections.
	now := time.Date(2026, 8, 21, 15, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)

	poolConfig, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	poolConfig.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// ARRANGE 2 — observable external boundaries. This storage fake blocks the first
	// HeadObject until release is called, so both Confirm calls can overlap without a
	// time.Sleep. The extractor counter must remain zero for Biometric Capture.
	objectStorage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "checkpoint-6-biometric-etag",
	})
	t.Cleanup(objectStorage.release)
	extractor := &concurrentBiometricFailFastExtractor{}

	// ARRANGE 3 — public application service with the existing PostgreSQL coordinator.
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(
		newArtifactConfirmAcquireFunc(pool),
	)
	service := artifact.NewService(
		postgresArtifacts,
		confirmCoordinator,
		objectStorage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	// ACT — release two ready callers on one start edge. The first HeadObject remains
	// blocked until both coordinator calls hold distinct pool connections, which makes
	// the overlap deterministic without time.Sleep or a direct pg_locks query.
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	start := make(chan struct{})
	results := make(chan confirmResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)

	for range 2 {
		go func() {
			ready.Done()
			select {
			case <-start:
			case <-testCtx.Done():
				results <- confirmResult{err: testCtx.Err()}
				return
			}

			summary, confirmErr := service.Confirm(
				testCtx,
				fixture.sessionID,
				fixture.rawToken,
				fixture.biometricIntentID,
			)
			results <- confirmResult{summary: summary, err: confirmErr}
		}()
	}

	ready.Wait()
	close(start)

	select {
	case <-objectStorage.entered:
	case <-testCtx.Done():
		t.Fatalf("first Confirm did not reach HeadObject: %v", testCtx.Err())
	}
	requireArtifactConfirmConnections(t, testCtx, pool, 2)
	objectStorage.release()

	collected := make([]confirmResult, 0, 2)
	for range 2 {
		select {
		case result := <-results:
			collected = append(collected, result)
		case <-testCtx.Done():
			t.Fatalf("concurrent Confirm calls did not finish: %v", testCtx.Err())
		}
	}

	// ASSERT 1 — both callers observe the same recorded public success.
	for caller, result := range collected {
		if result.err != nil {
			t.Errorf("Confirm() caller %d error = %v, want nil", caller+1, result.err)
		}
		if result.summary.ID != fixture.sessionID ||
			result.summary.Status != session.StatusBiometricCaptureUploaded ||
			!result.summary.ExpiresAt.Equal(fixture.sessionExpiresAt) {
			t.Errorf(
				"Confirm() caller %d summary = %#v, want session %s in %s until %s",
				caller+1,
				result.summary,
				fixture.sessionID,
				session.StatusBiometricCaptureUploaded,
				fixture.sessionExpiresAt,
			)
		}
	}
	if collected[0].summary != collected[1].summary {
		t.Errorf(
			"concurrent summaries differ: first=%#v second=%#v",
			collected[0].summary,
			collected[1].summary,
		)
	}

	// ASSERT 2 — only the winning caller performs external work.
	if calls := objectStorage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}

	// ASSERT 3 — PostgreSQL contains one complete accepted biometric outcome.
	var intentStatus string
	var sessionStatus string
	var confirmedIntentCount int
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			ui.status,
			vs.status,
			(SELECT count(*)
			 FROM upload_intents confirmed_ui
			 WHERE confirmed_ui.verification_session_id = vs.id
			   AND confirmed_ui.kind = 'biometric_capture'
			   AND confirmed_ui.status = 'confirmed'),
			(SELECT count(*)
			 FROM verification_artifacts va
			 WHERE va.upload_intent_id = ui.id
			   AND va.verification_session_id = vs.id
			   AND va.kind = 'biometric_capture'),
			(SELECT count(*)
			 FROM session_events se
			 WHERE se.session_id = vs.id
			   AND se.event_type = 'confirm_biometric_capture')
		FROM upload_intents ui
		JOIN verification_sessions vs
		  ON vs.id = ui.verification_session_id
		WHERE ui.id = $1
		  AND ui.verification_session_id = $2
		  AND ui.kind = 'biometric_capture'
	`, fixture.biometricIntentID, fixture.sessionID).Scan(
		&intentStatus,
		&sessionStatus,
		&confirmedIntentCount,
		&artifactCount,
		&eventCount,
	); err != nil {
		t.Fatalf("read concurrent Biometric Capture outcome: %v", err)
	}
	if intentStatus != "confirmed" {
		t.Errorf("Biometric Capture Upload Intent status = %q, want confirmed", intentStatus)
	}
	if confirmedIntentCount != 1 {
		t.Errorf("confirmed Biometric Capture Upload Intent count = %d, want 1", confirmedIntentCount)
	}
	if sessionStatus != session.StatusBiometricCaptureUploaded.String() {
		t.Errorf(
			"Verification Session status = %q, want %q",
			sessionStatus,
			session.StatusBiometricCaptureUploaded,
		)
	}
	if artifactCount != 1 {
		t.Errorf("accepted Biometric Capture Verification Artifact count = %d, want 1", artifactCount)
	}
	if eventCount != 1 {
		t.Errorf("confirm_biometric_capture Session Event count = %d, want 1", eventCount)
	}

	readyForSubmission, err := postgresArtifacts.HasRequiredAcceptedArtifacts(
		ctx,
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if !readyForSubmission {
		t.Error("HasRequiredAcceptedArtifacts() = false, want true")
	}
}

func requireArtifactConfirmConnections(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	want int32,
) {
	t.Helper()
	for pool.Stat().AcquiredConns() < want {
		select {
		case <-ctx.Done():
			t.Fatalf(
				"artifact Confirm callers did not acquire %d distinct pool connections: acquired=%d: %v",
				want,
				pool.Stat().AcquiredConns(),
				ctx.Err(),
			)
		default:
			runtime.Gosched()
		}
	}
}

type concurrentBiometricFailFastExtractor struct {
	mu    sync.Mutex
	calls int
}

func (d *concurrentBiometricFailFastExtractor) Extract(
	context.Context,
	string,
) (artifact.DocumentExtraction, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return artifact.DocumentExtraction{}, errors.New(
		"DocumentExtractor must not run for Biometric Capture",
	)
}

func (d *concurrentBiometricFailFastExtractor) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}
