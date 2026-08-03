package integration_test

import "testing"

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
func checkpoint6ConcurrentConfirmReturnsRecordedSuccessOnce(t *testing.T) {
	t.Fatal("Checkpoint 6 workbench: confirm the lock key, rename this function, then complete one numbered section at a time")
}
