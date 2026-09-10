package integration_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/session"
)

// checkpoint6ConcurrentConfirmReturnsRecordedSuccessOnce is the Checkpoint 6 user
// workbench. Rename it to
// TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce only
// after the advisory-lock key decision has been written in the checkpoint guide.
//
// Keep this first cycle to one public behavior: two concurrent Confirm calls for the
// same pending Upload Intent both observe the same accepted outcome, while PostgreSQL
// stores one outcome and each external boundary runs exactly once.
//
// ARRANGE 1 — real PostgreSQL
//
//  1. Start disposable PostgreSQL with openArtifactDatabase.
//  2. Seed one personal_details_submitted Verification Session, matching immutable
//     Personal Details, and one pending identity_document Upload Intent.
//  3. Create a pgxpool.Pool from the disposable database connection string. The two
//     Confirm calls must not share one raw *pgx.Conn concurrently.
//
// ARRANGE 2 — observable external boundaries
//
//  4. Add small HeadObject and DocumentExtractor fakes in this file. Protect their
//     call counts with sync.Mutex or atomic.Int64.
//  5. Return a non-empty image/jpeg ObjectMetadata and an extraction whose identity
//     number matches the seeded Personal Details.
//  6. Do not put the advisory lock, database writes, or artificial winner selection
//     inside the fakes. Do not use time.Sleep.
//
// ARRANGE 3 — public service
//
//  7. Build the real PostgreSQL artifact adapter from the pool and construct one
//     artifact.Service. Let the first compile failure drive the smallest coordinator
//     interface needed for a dedicated locked connection; do not expose pgx types
//     from the application package.
//
// ACT — two callers, one start edge
//
//  8. Start two goroutines. Use a ready WaitGroup so both are waiting on the same
//     start channel, then close(start) exactly once.
//  9. Each goroutine calls service.Confirm with the same session ID, resume token,
//     and Upload Intent ID, and sends exactly one {summary, err} result to a buffered
//     channel. Wait for both callers before asserting.
//
// # ASSERT — returned and durable behavior
//
//  10. Require two nil errors and two identical identity_document_uploaded summaries.
//     A CONFIRMATION_STALE second result is the expected current RED, not the contract.
//  11. Query committed PostgreSQL state: Upload Intent confirmed, one Verification
//     Artifact, one confirm_identity_document event, and the advanced session state.
//  12. Read the concurrency-safe counters after both callers finish. Require exactly
//     one HeadObject and one extraction.
//
// RED first, then implement only enough dedicated-connection advisory coordination
// and confirmed replay behavior to make this tracer GREEN. Stop for agent review
// before adding mismatch, expiry, cancellation, external-failure, or MinIO siblings.
type confirmResult struct {
	summary session.Summary
	err     error
}

type integrationConfirmConnLease struct {
	*pgxpool.Conn
}

func (l *integrationConfirmConnLease) Discard(ctx context.Context) error {
	conn := l.Hijack()
	return conn.Close(ctx)
}

func newArtifactConfirmAcquireFunc(
	pool *pgxpool.Pool,
) postgresadapter.AcquireFunc {
	return func(
		ctx context.Context,
	) (postgresadapter.ConnLease, error) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}

		return &integrationConfirmConnLease{
			Conn: conn,
		}, nil
	}
}

func TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce(t *testing.T) {
	now := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)

	f := seedArtifactConfirmationState(t, ctx, database, now)

	connString := database.Config().ConnString()
	cfg, err := pgxpool.ParseConfig(connString)

	if err != nil {
		t.Fatalf("ParseConfig error: %v", err)
	}
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("new pool error: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
	})

	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool))
	tokens := session.NewProductionCryptoTokens()

	objectStorage := &concurrentObjectStorage{
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1064,
			ETag:        "image-etag",
		},
	}

	extraction := &concurrentDocumentExtractor{
		extraction: artifact.DocumentExtraction{
			IdentityNumber: f.identityNumber,
		},
	}

	service := artifact.NewService(postgresArtifacts, confirmCoordinator, objectStorage, extraction, tokens, fixedClock{now: now})

	var ready sync.WaitGroup
	var callers sync.WaitGroup
	ready.Add(2)
	callers.Add(2)

	start := make(chan struct{})
	summaries := make(chan confirmResult, 2)

	for range 2 {
		go func() {
			defer callers.Done()

			ready.Done()
			<-start

			summary, err := service.Confirm(ctx, f.sessionID, f.rawToken, f.uploadIntentID)
			summaries <- confirmResult{
				summary: summary,
				err:     err,
			}
		}()
	}

	ready.Wait()
	close(start)

	callers.Wait()
	close(summaries)

	collectedSummaries := make([]confirmResult, 0, 2)
	for r := range summaries {
		collectedSummaries = append(collectedSummaries, r)
	}

	if len(collectedSummaries) != 2 {
		t.Fatalf("must have 2 summaries, got %d", len(collectedSummaries))
	}

	if objectStorage.calls != 1 || extraction.calls != 1 {
		t.Errorf("objectStorage call and extraction call must be 1, objectStorage got %d, extraction got %d", objectStorage.calls, extraction.calls)
	}

	first := collectedSummaries[0]
	second := collectedSummaries[1]

	if first.err != nil || second.err != nil {
		t.Errorf("both error should be nil, got %v and %v", first.err, second.err)
	}

	firstSum := first.summary
	secondSum := second.summary

	if !firstSum.ExpiresAt.Equal(secondSum.ExpiresAt) || firstSum.ID != secondSum.ID || firstSum.Status != secondSum.Status {
		t.Errorf("both summary must be identic, got firstSummary %v, secondSummary %v", firstSum, secondSum)
	}

	if firstSum.ID != f.sessionID || firstSum.Status != "identity_document_uploaded" || !firstSum.ExpiresAt.Equal(now.Add(30*time.Minute)) {
		t.Errorf("data from first summary wrong, got %v", firstSum)
	}

	if secondSum.ID != f.sessionID || secondSum.Status != "identity_document_uploaded" || !secondSum.ExpiresAt.Equal(now.Add(30*time.Minute)) {
		t.Errorf("data from second summary wrong, got %v", secondSum)
	}

	var uploadIntentStatus string
	var storedSessionStatus string
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			ui.status,
			vs.status,
			(
				SELECT count(*)
				FROM verification_artifacts va
				WHERE va.upload_intent_id = ui.id
			),
			(
				SELECT count(*)
				FROM session_events se
				WHERE se.session_id = ui.verification_session_id
				  AND se.event_type = 'confirm_identity_document'
			)
		FROM upload_intents ui
		JOIN verification_sessions vs
		  ON vs.id = ui.verification_session_id
		WHERE ui.id = $1
		  AND ui.verification_session_id = $2
	`, f.uploadIntentID, f.sessionID).Scan(
		&uploadIntentStatus,
		&storedSessionStatus,
		&artifactCount,
		&eventCount,
	); err != nil {
		t.Fatalf("read concurrent confirm durable outcome: %v", err)
	}

	if uploadIntentStatus != "confirmed" {
		t.Errorf("Upload Intent status = %q, want confirmed", uploadIntentStatus)
	}
	if artifactCount != 1 {
		t.Errorf("Verification Artifact count = %d, want 1", artifactCount)
	}
	if eventCount != 1 {
		t.Errorf("confirm_identity_document event count = %d, want 1", eventCount)
	}
	if storedSessionStatus != session.StatusIdentityDocumentUploaded.String() {
		t.Errorf(
			"Verification Session status = %q, want %q",
			storedSessionStatus,
			session.StatusIdentityDocumentUploaded,
		)
	}

}

func TestPostgresConcurrentConfirmSecondCallerWaitsForSameAdvisoryLock(t *testing.T) {
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)

	config, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse artifact pool config: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "deterministic-lock-proof-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	coordinator := postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool))
	service := artifact.NewService(
		postgresArtifacts,
		coordinator,
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	results := make(chan confirmResult, 2)
	callConfirm := func() {
		summary, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		results <- confirmResult{summary: summary, err: confirmErr}
	}

	go callConfirm()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Confirm did not reach HeadObject")
	}

	go callConfirm()
	requireAdvisoryLockWaiter(t, ctx, database)
	storage.release()

	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Errorf("Confirm() error = %v, want nil", result.err)
			}
			if result.summary.ID != fixture.sessionID ||
				result.summary.Status != session.StatusIdentityDocumentUploaded {
				t.Errorf("Confirm() summary = %#v, want recorded success", result.summary)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent Confirm calls did not finish")
		}
	}

	if storage.callCount() != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", storage.callCount())
	}
	if extractor.calls != 1 {
		t.Errorf("Extract() calls = %d, want 1", extractor.calls)
	}
}

func TestPostgresConfirmedReplayReturnsRecordedSuccessWithoutExternalWork(t *testing.T) {
	now := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := &concurrentObjectStorage{metadata: artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "confirmed-replay-etag",
	}}
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	first, err := service.Confirm(ctx, fixture.sessionID, fixture.rawToken, fixture.uploadIntentID)
	if err != nil {
		t.Fatalf("first Confirm() error = %v", err)
	}
	second, err := service.Confirm(ctx, fixture.sessionID, fixture.rawToken, fixture.uploadIntentID)
	if err != nil {
		t.Fatalf("replay Confirm() error = %v", err)
	}
	if first != second {
		t.Errorf("replay summary = %#v, want %#v", second, first)
	}
	if storage.calls != 1 || extractor.calls != 1 {
		t.Errorf(
			"external calls = HeadObject:%d Extract:%d, want 1 each",
			storage.calls,
			extractor.calls,
		)
	}

	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM verification_artifacts WHERE upload_intent_id = $1),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $2 AND event_type = 'confirm_identity_document')
	`, fixture.uploadIntentID, fixture.sessionID).Scan(&artifactCount, &eventCount); err != nil {
		t.Fatalf("read confirmed replay durable outcome: %v", err)
	}
	if artifactCount != 1 || eventCount != 1 {
		t.Errorf("durable counts = artifacts:%d events:%d, want 1 each", artifactCount, eventCount)
	}
}

func TestPostgresValidationFailedReplayReturnsRecordedFailureWithoutExternalWork(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := &concurrentObjectStorage{metadata: artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "validation-failed-replay-etag",
	}}
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: "different-identity-number",
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	_, firstErr := service.Confirm(ctx, fixture.sessionID, fixture.rawToken, fixture.uploadIntentID)
	requireIdentityMismatchError(t, firstErr)
	_, replayErr := service.Confirm(ctx, fixture.sessionID, fixture.rawToken, fixture.uploadIntentID)
	requireIdentityMismatchError(t, replayErr)

	if storage.calls != 1 || extractor.calls != 1 {
		t.Errorf(
			"external calls = HeadObject:%d Extract:%d, want 1 each",
			storage.calls,
			extractor.calls,
		)
	}

	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM verification_artifacts WHERE upload_intent_id = $1),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $2 AND event_type = 'confirm_identity_document')
	`, fixture.uploadIntentID, fixture.sessionID).Scan(&artifactCount, &eventCount); err != nil {
		t.Fatalf("read validation-failed replay durable outcome: %v", err)
	}
	if artifactCount != 0 || eventCount != 1 {
		t.Errorf("durable counts = artifacts:%d events:%d, want 0 and 1", artifactCount, eventCount)
	}
}

func TestConcurrentPostgresConfirmMismatchReturnsRecordedFailureAndRunsExternalWorkOnce(t *testing.T) {
	now := time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := &concurrentObjectStorage{metadata: artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "concurrent-mismatch-etag",
	}}
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: "different-identity-number",
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	var callers sync.WaitGroup
	ready.Add(2)
	callers.Add(2)
	for range 2 {
		go func() {
			defer callers.Done()
			ready.Done()
			<-start
			_, confirmErr := service.Confirm(
				ctx,
				fixture.sessionID,
				fixture.rawToken,
				fixture.uploadIntentID,
			)
			results <- confirmErr
		}()
	}
	ready.Wait()
	close(start)
	callers.Wait()
	close(results)

	for confirmErr := range results {
		requireIdentityMismatchError(t, confirmErr)
	}
	if storage.calls != 1 || extractor.calls != 1 {
		t.Errorf(
			"external calls = HeadObject:%d Extract:%d, want 1 each",
			storage.calls,
			extractor.calls,
		)
	}

	var status string
	var failureCode string
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			ui.status,
			ui.failure_code,
			(SELECT count(*) FROM verification_artifacts WHERE upload_intent_id = ui.id),
			(SELECT count(*) FROM session_events
			 WHERE session_id = ui.verification_session_id
			   AND event_type = 'confirm_identity_document')
		FROM upload_intents ui
		WHERE ui.id = $1
	`, fixture.uploadIntentID).Scan(&status, &failureCode, &artifactCount, &eventCount); err != nil {
		t.Fatalf("read concurrent mismatch outcome: %v", err)
	}
	if status != "validation_failed" ||
		failureCode != string(artifact.ReasonIdentityNumberMismatch) ||
		artifactCount != 0 || eventCount != 1 {
		t.Errorf(
			"durable mismatch = status:%q failure:%q artifacts:%d events:%d",
			status,
			failureCode,
			artifactCount,
			eventCount,
		)
	}
}

func TestPostgresConfirmExternalFailureReleasesLockForSuccessfulRetry(t *testing.T) {
	now := time.Date(2026, 8, 11, 14, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storageErr := errors.New("injected object storage failure")
	storage := &retryObjectStorage{
		firstErr: storageErr,
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "retry-success-etag",
		},
	}
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	_, firstErr := service.Confirm(ctx, fixture.sessionID, fixture.rawToken, fixture.uploadIntentID)
	var artifactErr *artifact.Error
	if !errors.As(firstErr, &artifactErr) || artifactErr.Code != artifact.CodeObjectStorageFailed {
		t.Fatalf("first Confirm() error = %#v, want OBJECT_STORAGE_FAILED", firstErr)
	}

	retryResult := make(chan confirmResult, 1)
	go func() {
		summary, retryErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		retryResult <- confirmResult{summary: summary, err: retryErr}
	}()
	select {
	case result := <-retryResult:
		if result.err != nil {
			t.Fatalf("retry Confirm() error = %v", result.err)
		}
		if result.summary.Status != session.StatusIdentityDocumentUploaded {
			t.Errorf("retry summary = %#v, want success", result.summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry Confirm remained blocked after external failure")
	}

	if storage.callCount() != 2 || extractor.calls != 1 {
		t.Errorf(
			"external calls = HeadObject:%d Extract:%d, want 2 and 1",
			storage.callCount(),
			extractor.calls,
		)
	}
}

func TestPostgresConfirmCancellationWhileWaitingDoesNotLeakConnectionOrLock(t *testing.T) {
	now := time.Date(2026, 8, 11, 15, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	config, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse artifact pool config: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "cancellation-lock-proof-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	firstResult := make(chan confirmResult, 1)
	go func() {
		summary, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		firstResult <- confirmResult{summary: summary, err: confirmErr}
	}()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Confirm did not reach HeadObject")
	}

	waiterCtx, cancelWaiter := context.WithCancel(ctx)
	secondResult := make(chan error, 1)
	go func() {
		_, confirmErr := service.Confirm(
			waiterCtx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		secondResult <- confirmErr
	}()
	requireAdvisoryLockWaiter(t, ctx, database)
	cancelWaiter()

	select {
	case confirmErr := <-secondResult:
		if !errors.Is(confirmErr, context.Canceled) {
			t.Fatalf("waiting Confirm() error = %v, want context canceled", confirmErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled advisory-lock waiter did not return")
	}

	storage.release()
	select {
	case result := <-firstResult:
		if result.err != nil {
			t.Fatalf("first Confirm() error = %v", result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Confirm did not finish after waiter cancellation")
	}

	replayResult := make(chan error, 1)
	go func() {
		_, replayErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		replayResult <- replayErr
	}()
	select {
	case replayErr := <-replayResult:
		if replayErr != nil {
			t.Fatalf("post-cancellation replay error = %v", replayErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-cancellation replay remained blocked")
	}
}

func TestPostgresConcurrentConfirmDiscardsExternalResultWhenIntentIsSuperseded(t *testing.T) {
	now := time.Date(2026, 8, 11, 16, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	config, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse artifact pool config: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "superseded-stale-result-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	results := make(chan error, 2)
	callConfirm := func() {
		_, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		results <- confirmErr
	}
	go callConfirm()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Confirm did not reach HeadObject")
	}
	go callConfirm()
	requireAdvisoryLockWaiter(t, ctx, database)

	if _, err := database.Exec(ctx, `
		UPDATE upload_intents
		SET status = 'superseded', latest_status_change_at = $2
		WHERE id = $1
	`, fixture.uploadIntentID, now); err != nil {
		t.Fatalf("supersede Upload Intent while Confirm waits: %v", err)
	}
	storage.release()

	for range 2 {
		select {
		case confirmErr := <-results:
			requireArtifactErrorCode(t, confirmErr, artifact.CodeUploadIntentSuperseded)
		case <-time.After(5 * time.Second):
			t.Fatal("superseded Confirm calls did not finish")
		}
	}
	requireNoArtifactOutcome(t, ctx, database, fixture.sessionID, fixture.uploadIntentID)
	if storage.callCount() != 1 || extractor.calls != 1 {
		t.Errorf(
			"external calls = HeadObject:%d Extract:%d, want 1 each",
			storage.callCount(),
			extractor.calls,
		)
	}
}

func TestPostgresConcurrentConfirmDiscardsExternalResultWhenIntentExpires(t *testing.T) {
	now := time.Date(2026, 8, 11, 17, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	config, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse artifact pool config: %v", err)
	}
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "expired-stale-result-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	results := make(chan error, 2)
	callConfirm := func() {
		_, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		results <- confirmErr
	}
	go callConfirm()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Confirm did not reach HeadObject")
	}
	go callConfirm()
	requireAdvisoryLockWaiter(t, ctx, database)

	if _, err := database.Exec(ctx, `
		UPDATE upload_intents
		SET expires_at = $2
		WHERE id = $1
	`, fixture.uploadIntentID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("expire Upload Intent during external validation: %v", err)
	}
	storage.release()

	for range 2 {
		select {
		case confirmErr := <-results:
			requireArtifactErrorCode(t, confirmErr, artifact.CodeUploadIntentExpired)
		case <-time.After(5 * time.Second):
			t.Fatal("expired Confirm calls did not finish")
		}
	}
	requireNoArtifactOutcome(t, ctx, database, fixture.sessionID, fixture.uploadIntentID)
}

func TestPostgresCreateIdentityUploadIntentSupersedesInFlightConfirm(t *testing.T) {
	now := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedArtifactConfirmationState(t, ctx, database, now)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("open artifact pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "create-versus-confirm-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentDocumentExtractor{extraction: artifact.DocumentExtraction{
		IdentityNumber: fixture.identityNumber,
	}}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	confirmService := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)
	presigner := &artifactPostgresPresigner{url: "https://uploads.example.test/replacement"}
	createService := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	confirmResult := make(chan error, 1)
	go func() {
		_, confirmErr := confirmService.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.uploadIntentID,
		)
		confirmResult <- confirmErr
	}()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Confirm did not reach HeadObject")
	}

	created, err := createService.Create(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		"identity_document",
	)
	if err != nil {
		t.Fatalf("Create() during Confirm error = %v", err)
	}
	storage.release()

	select {
	case confirmErr := <-confirmResult:
		requireArtifactErrorCode(t, confirmErr, artifact.CodeUploadIntentSuperseded)
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight Confirm did not finish")
	}
	requireNoArtifactOutcome(t, ctx, database, fixture.sessionID, fixture.uploadIntentID)

	var oldStatus string
	var replacementStatus string
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT status FROM upload_intents WHERE id = $1),
			(SELECT status FROM upload_intents WHERE id = $2)
	`, fixture.uploadIntentID, created.ID).Scan(&oldStatus, &replacementStatus); err != nil {
		t.Fatalf("read create-versus-confirm outcome: %v", err)
	}
	if oldStatus != "superseded" || replacementStatus != "pending" {
		t.Errorf(
			"intent statuses = old:%q replacement:%q, want superseded and pending",
			oldStatus,
			replacementStatus,
		)
	}
}

func requireArtifactErrorCode(t *testing.T, err error, code artifact.ErrorCode) {
	t.Helper()
	var artifactErr *artifact.Error
	if !errors.As(err, &artifactErr) || artifactErr.Code != code {
		t.Fatalf("error = %#v, want artifact code %s", err, code)
	}
}

func requireNoArtifactOutcome(
	t *testing.T,
	ctx context.Context,
	database interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	sessionID uuid.UUID,
	uploadIntentID uuid.UUID,
) {
	t.Helper()
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM verification_artifacts WHERE upload_intent_id = $1),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $2 AND event_type = 'confirm_identity_document')
	`, uploadIntentID, sessionID).Scan(&artifactCount, &eventCount); err != nil {
		t.Fatalf("read absent confirmation outcome: %v", err)
	}
	if artifactCount != 0 || eventCount != 0 {
		t.Errorf("durable counts = artifacts:%d events:%d, want 0 each", artifactCount, eventCount)
	}
}

func requireIdentityMismatchError(t *testing.T, err error) {
	t.Helper()
	var artifactErr *artifact.Error
	if !errors.As(err, &artifactErr) ||
		artifactErr.Code != artifact.CodeLocalValidationFailed ||
		artifactErr.Reason != artifact.ReasonIdentityNumberMismatch {
		t.Fatalf("Confirm() error = %#v, want recorded identity-number mismatch", err)
	}
}

func requireAdvisoryLockWaiter(t *testing.T, ctx context.Context, database interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var grantedCount int
		var waitingCount int
		err := database.QueryRow(waitCtx, `
			SELECT
				count(*) FILTER (WHERE granted),
				count(*) FILTER (WHERE NOT granted)
			FROM pg_locks
			WHERE locktype = 'advisory'
		`).Scan(&grantedCount, &waitingCount)
		if err != nil {
			t.Fatalf("inspect PostgreSQL advisory locks: %v", err)
		}
		if grantedCount >= 1 && waitingCount >= 1 {
			return
		}

		select {
		case <-waitCtx.Done():
			t.Fatalf(
				"PostgreSQL advisory lock state never showed a waiter: granted=%d waiting=%d",
				grantedCount,
				waitingCount,
			)
		default:
			runtime.Gosched()
		}
	}
}

type concurrentObjectStorage struct {
	metadata artifact.ObjectMetadata
	calls    int
	mu       sync.Mutex
	err      error
}

type blockingConcurrentObjectStorage struct {
	metadata    artifact.ObjectMetadata
	entered     chan struct{}
	releaseGate chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	calls       int
}

type retryObjectStorage struct {
	firstErr error
	metadata artifact.ObjectMetadata
	mu       sync.Mutex
	calls    int
}

func (o *retryObjectStorage) HeadObject(
	context.Context,
	string,
) (artifact.ObjectMetadata, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	if o.calls == 1 {
		return artifact.ObjectMetadata{}, o.firstErr
	}
	return o.metadata, nil
}

func (o *retryObjectStorage) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func newBlockingConcurrentObjectStorage(metadata artifact.ObjectMetadata) *blockingConcurrentObjectStorage {
	return &blockingConcurrentObjectStorage{
		metadata:    metadata,
		entered:     make(chan struct{}),
		releaseGate: make(chan struct{}),
	}
}

func (o *blockingConcurrentObjectStorage) HeadObject(
	ctx context.Context,
	_ string,
) (artifact.ObjectMetadata, error) {
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()
	o.enterOnce.Do(func() { close(o.entered) })

	select {
	case <-o.releaseGate:
		return o.metadata, nil
	case <-ctx.Done():
		return artifact.ObjectMetadata{}, ctx.Err()
	}
}

func (o *blockingConcurrentObjectStorage) release() {
	o.releaseOnce.Do(func() { close(o.releaseGate) })
}

func (o *blockingConcurrentObjectStorage) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func (o *concurrentObjectStorage) HeadObject(ctx context.Context, storageKey string) (artifact.ObjectMetadata, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++

	return o.metadata, o.err
}

type concurrentDocumentExtractor struct {
	extraction artifact.DocumentExtraction
	calls      int
	mu         sync.Mutex
	err        error
}

func (d *concurrentDocumentExtractor) Extract(ctx context.Context, storageKey string) (artifact.DocumentExtraction, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++

	return d.extraction, d.err
}
