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
