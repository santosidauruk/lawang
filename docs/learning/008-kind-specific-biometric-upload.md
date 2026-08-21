# Kind-Specific Biometric Upload

This is the working learning record for Issue 008. Conclusions are recorded only
after the corresponding RED-GREEN checkpoint has been exercised and reviewed.

## Checkpoint 1: kind policy and Biometric Upload Intent

Checkpoint 1 is complete. The retained user tracer proves that
`UploadIntentService.Create` accepts exact kind `biometric_capture` only when the
Verification Session is `identity_document_uploaded`. It generates a fresh Upload
Intent UUID, derives
`verification-sessions/{sessionID}/biometric_capture/{intentID}`, presigns for five
minutes before opening a database transaction, then re-reads the session and
atomically supersedes/inserts the kind-scoped pending intent.

The initial state differs by evidence kind because the public workflow is ordered:
Identity Document upload authorization follows Personal Details, while Biometric
Capture upload authorization follows an accepted Identity Document. This state rule
is kind-specific. UUID generation, five-minute TTL, external-I/O ordering, guarded
transaction, and supersession by `(session, kind)` are shared invariants.

The implementation keeps the two allowed kinds and their required states explicit.
It does not introduce a registry or generic artifact framework. Exact kind is safe as
the storage-key segment only after invalid kinds have been rejected before session,
presigner, or transaction work.

Application sibling tests prove wrong initial state, stale transactional re-read,
presign failure without writes, preservation of confirmed Identity Document intent
history, repeated-create freshness, identity/biometric key isolation, and invalid-kind
short-circuiting. The memory transaction proves application orchestration only; real
one-pending-per-kind behavior under race was deferred to PostgreSQL and is covered by
the Checkpoint 3 regression gates below.
These sibling tests were GREEN on their first run because the reviewed user minimal
GREEN already covered the shared flow; no artificial RED or additional production
change was introduced.

Verification evidence on 2026-08-13:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./internal/application/artifact
PASS

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./...
PASS

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make staticcheck
PASS
```

## Checkpoint 2: application confirmation without extraction

Checkpoint 2 is complete. The retained user tracer proves that the owned pending
Upload Intent selects Biometric Capture behavior through the existing public
`artifact.Service.Confirm` method. No request-supplied kind or parallel biometric
service was added.

The application checks real object metadata before the short database transaction.
Biometric Capture accepts JPEG/PNG, requires a non-zero object, and uses an inclusive
5 MiB maximum. PDF and other content types are rejected with bounded
`INVALID_OBJECT_METADATA` reasons. Identity Document keeps its existing inclusive
10 MiB JPEG/PNG/PDF policy.

Biometric confirmation does not load immutable Personal Details and never calls the
DocumentExtractor. The short transaction re-reads the session and intent, rejects a
changed kind before any kind-specific work, then commits the confirmed intent,
accepted Biometric Verification Artifact, `biometric_capture_uploaded` state, and
accepted `confirm_biometric_capture` event as one unit. Injecting final event failure
rolls back all memory effects. Session/token/intent/key/expiry changes during
`HeadObject` discard the external result without partial writes.

Two sibling tests produced meaningful RED evidence: 5 MiB plus one byte was initially
accepted by the Identity Document size policy, and PDF was initially accepted by the
shared content-type policy. A third RED showed that changing the intent kind during
external I/O reached a raw domain transition error; the guarded transaction now
rejects that change as `CONFIRMATION_STALE` before loading Personal Details.

Verification evidence on 2026-08-13:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate

ok github.com/santosidauruk/lawang-go/tests/integration
ok github.com/santosidauruk/lawang-go/tests/schema
```

## Checkpoint 3: PostgreSQL atomic biometric outcome

Checkpoint 3 is complete. The retained success tracer uses disposable PostgreSQL and
the public `artifact.Service.Confirm` path. Its fixture preserves a valid prior
Identity Document outcome, including immutable Personal Details, the confirmed
Identity Upload Intent, accepted Identity Verification Artifact, and ordered Session
Events. Confirmation reuses the existing PostgreSQL query and adapter boundaries; no
production SQL, migration, generated SQLC, or parallel biometric persistence stack
was required.

The committed outcome contains one confirmed Biometric Upload Intent, preserves the
Identity Verification Artifact, adds exactly one accepted Biometric Verification
Artifact, advances to `biometric_capture_uploaded`, and appends exactly one accepted
`confirm_biometric_capture` event. A test-only connection observer verifies that
`HeadObject` runs before the short database transaction, while a fail-fast extractor
proves Biometric Capture performs no document extraction.

Agent regressions prove forced event failure rolls back all biometric database
effects, create/supersede changes only pending biometric intents, and state/key/kind
changes during external I/O return `CONFIRMATION_STALE` without partial writes. The
existing schema proof remains authoritative for one artifact per intent, one accepted
artifact per `(session, kind)`, bounded metadata/kind, and composite ownership of
session, kind, and storage key. The complete Identity Document PostgreSQL suite stays
GREEN.

Verification evidence on 2026-08-14:

```text
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run 'Biometric.*Postgres|Postgres.*Biometric' -count=1 -v
ok github.com/santosidauruk/lawang-go/tests/integration 21.794s

GOCACHE=/tmp/lawang-go-build go test ./tests/schema \
  -run '^TestVerificationArtifactMigrationAndConstraintProof$' -count=1 -v
ok github.com/santosidauruk/lawang-go/tests/schema 8.656s

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate
```

## Checkpoint 4: artifact-derived submission readiness

Checkpoint 4 is complete. The retained user tracer proves one internal PostgreSQL
predicate: a Verification Session has the required accepted evidence only when the
same session owns both an `identity_document` and a `biometric_capture` Verification
Artifact. The query returns one boolean and does not inspect object storage, Upload
Intent status, or session state.

Standalone `true` is a snapshot, not an authorization or reservation. The same
adapter boundary can bind to a database handle or an existing `pgx.Tx`; Issue 009
must call it again inside the guarded Provider Submission transaction together with
the session-state and replay guards.

Agent sibling tests prove `false` for identity-only, biometric-only, a fake stored
object without accepted artifact, a confirmed Biometric Intent without artifact, a
validation-failed Identity Intent plus Biometric Artifact, and required artifacts
split across two sessions. All sibling behaviors were direct GREEN after the reviewed
success query, so no additional production branch or abstraction was introduced.

`sql/proofs/007_submission_readiness.sql` proves the same-session success predicate
and logs the actual PostgreSQL plan. The small disposable fixture chooses an
`Aggregate` over sequential scans; this does not support a production index-usage
claim. Existing composite ownership and unique `(verification_session_id, kind)`
constraints remain sufficient, so Checkpoint 4 adds no migration.

Verification evidence on 2026-08-14:

```text
GOCACHE=/tmp/lawang-go-build go test ./tests/integration ./tests/schema \
  -run 'SubmissionReadiness|AcceptedIdentityAndBiometricArtifactsMakeSessionSubmissionReady|VerificationArtifactMigrationAndConstraintProof' \
  -count=1
PASS

GOCACHE=/tmp/lawang-go-build go test -race ./internal/adapter/postgres -count=1
ok github.com/santosidauruk/lawang-go/internal/adapter/postgres

GOCACHE=/tmp/lawang-go-build go vet \
  ./internal/adapter/postgres ./tests/integration ./tests/schema
PASS

GOCACHE=/tmp/lawang-go-build make sqlc-diff
PASS: no generated diff

GOCACHE=/tmp/lawang-go-build \
  STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate
```

## Checkpoint 5: public HTTP, PostgreSQL, and real MinIO

Checkpoint 5 is complete. The retained user tracer crosses the public upload-url
handler, performs an unauthenticated HTTP PUT to the exact returned presigned URL,
confirms through the public handler, and observes the durable PostgreSQL result. Its
metadata assertion compares the stored artifact with metadata captured from the real
MinIO `HeadObject`, rather than repeating request constants.

The public success contract remains shared with Identity Document: upload-url returns
exactly `uploadIntentId` and `uploadUrl`, while confirm returns the current session
summary. The bounded upload-kind error now names the two supported kinds. A wrong
intent kind during confirmation uses kind-neutral wording; no biometric-specific
route or service was introduced.

Agent siblings prove real PNG success, readiness false before and true after the
accepted biometric artifact commits, and bounded rejection of PDF, empty, and
over-5-MiB objects based on real MinIO metadata. Wrong session state, expiry, and
supersession short-circuit before object storage. Missing objects and an unavailable
S3 endpoint map to the bounded storage error without durable biometric effects.
Every biometric path keeps DocumentExtractor at zero calls.

Verification evidence on 2026-08-21:

```text
Focused HTTP/PostgreSQL/MinIO biometric and Identity Document suite
PASS: tests/integration, 38.084s

GOCACHE=/tmp/lawang-go-build go test -race \
  ./internal/application/artifact ./internal/adapter/httpapi \
  ./internal/adapter/postgres -count=1
PASS

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate
ok github.com/santosidauruk/lawang-go/tests/integration 266.288s
```

## Checkpoint 6: replay, concurrency, and Identity regression

Checkpoint 6 is complete. The retained user tracer starts two public
`artifact.Service.Confirm` calls for the same pending Biometric Upload Intent against
disposable PostgreSQL. A blocking `HeadObject` rendezvous and a two-connection pool
prove both callers overlap without sharing a raw connection or using `time.Sleep`.
Both callers return the same recorded `biometric_capture_uploaded` summary while
external work runs once, DocumentExtractor remains unused, and PostgreSQL stores one
confirmed intent, accepted Biometric Verification Artifact, and
`confirm_biometric_capture` event. Readiness becomes true only from the accepted
Identity and Biometric artifact rows.

The tracer was direct GREEN, so no coordinator/adapter change was justified. The
existing coordinator remains kind-neutral and locks by Upload Intent ID on a pinned
connection. Agent regressions use `pg_locks` to observe one granted lock and one real
waiter, then prove sequential replay performs no extra work, cancellation of the
waiter does not release the winner's lock, and a later replay is not left blocked.

Supersede, expiry, and create-vs-confirm races discard the already-fetched biometric
metadata without committing partial effects or readiness. Four concurrent PostgreSQL
creates—two per kind—produce one pending winner and one unique-conflict per kind;
the two committed keys keep exact identity/biometric kind segments and distinct
intent UUIDs. The full Identity Document replay, mismatch, extraction, cancellation,
stale-result, and create-vs-confirm suite remains GREEN.

The only RED in agent work was a compile failure because a mutex-protected test fake
lacked a `callCount` accessor. The minimal GREEN added that test-only accessor. No
production code, migration, SQL query, generated SQLC, new coordinator, or new status
was needed.

Verification evidence on 2026-08-21:

```text
GOCACHE=/tmp/lawang-go-build go test -race ./tests/integration \
  -run '^(TestConcurrentBiometricPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce|TestPostgresBiometric.*|TestPostgresCreateBiometric.*|TestPostgresConcurrentCreateKeeps.*)$' \
  -count=1 -v
PASS: 8 tests, tests/integration 74.124s

Focused Identity Document replay/mismatch/extractor/concurrency suite
PASS: 10 tests, tests/integration 83.574s

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate
ok github.com/santosidauruk/lawang-go/tests/integration 601.513s
```
