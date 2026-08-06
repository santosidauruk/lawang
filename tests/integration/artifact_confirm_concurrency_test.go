package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
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
//
//lint:ignore U1000 This Checkpoint 6 user workbench intentionally remains dormant.

type confirmResult struct {
	summary session.Summary
	err     error
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

	service := artifact.NewService(postgresArtifacts, postgresArtifacts, objectStorage, extraction, tokens, fixedClock{now: now})

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

}

type concurrentObjectStorage struct {
	metadata artifact.ObjectMetadata
	calls    int
	mu       sync.Mutex
	err      error
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
