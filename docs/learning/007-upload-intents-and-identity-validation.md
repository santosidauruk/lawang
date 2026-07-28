# Upload Intents and Identity Validation

This is the working learning record for Issue 007. Conclusions are recorded only
after the corresponding RED-GREEN checkpoint has been exercised and reviewed.

## Checkpoint 1: Upload Intent schema and concurrency

Checkpoint 1 is complete. The reviewed schema establishes these decisions:

- Upload constraints are code-owned: the row stores bounded `kind`, while Go maps
  `identity_document` to JPEG/PNG/PDF, non-zero, and at most 10 MiB. Pending intents
  use the currently deployed mapping rather than a snapshotted rule set.
- The application generates the Upload Intent UUID and uses it to construct a fresh
  storage key before persistence. It presigns outside a transaction. Only after
  signing succeeds does a short transaction re-read guards, supersede the previous
  pending intent, and insert the new row. Signing failure writes nothing; a stale
  transactional re-read discards the unreturned URL.
- An Upload Intent is permission and lifecycle state for one attempted upload. The
  stored object is the file addressed by `storage_key`; it may not exist yet when the
  intent is created. A Verification Artifact is accepted evidence created only after
  confirmation inspects and validates that object. These are three different things.
- Migration `00005` bounds kind, status, failure code, confirmation timestamp, and
  cleanup state; requires unique non-empty storage keys; and uses a partial unique
  index to permit at most one `pending` row for each `(verification_session_id, kind)`
  while retaining non-pending history.

Verification evidence on 2026-07-20:

- `005_upload_intent_constraints.sql` passed on disposable PostgreSQL 18. It proved
  valid defaults, bounded values, coherent status metadata, cleanup eligibility,
  unique storage keys, multiple historical rows, and replacement with an updated
  `latest_status_change_at`.
- `TestConcurrentPendingUploadIntentCreationAllowsExactlyOneWinner` used two distinct
  `pgx.Conn` sessions released by the same start signal, with no timing sleeps. One
  insert committed; the other received SQLSTATE `23505` from
  `upload_intents_one_pending_per_session_kind_idx`; the committed final state had
  exactly one pending Identity Document intent.
- PostgreSQL may make the losing insert wait briefly while the competing insert is
  unresolved. After the winner commits, the partial unique index rejects the loser.
  The Go race detector also passed for this test.

## Checkpoint 2: application confirmation

Checkpoint 2 is complete. The user-authored success tracer and guarded memory
transaction were retained, then sibling behavior was added one RED-GREEN cycle at a
time.

`HeadObject` and extraction execute before the transaction because their latency and
failure modes are external to PostgreSQL. Holding row locks while waiting for them
would lengthen contention and still would not make object storage atomic with the
database. The short transaction therefore locks and re-reads the session, Upload
Intent, and immutable Personal Details. It commits only when the external result still
matches the guarded storage key and other authorization, lifecycle, and identity
facts; otherwise it discards that result without database-like effects.

The application tests now prove:

- accepted confirmation commits the intent, Verification Artifact, state transition,
  and safe event as one unit;
- identity-number mismatch commits only `validation_failed` plus one bounded event and
  returns `LOCAL_VALIDATION_FAILED` with `identity_number_mismatch`;
- invalid token and exact session expiry stop before intent/details and external I/O;
- missing, expired, superseded, or wrong-kind intents return bounded errors before
  external I/O;
- JPEG, PNG, and PDF are accepted when non-empty and no larger than 10 MiB, including
  the exact maximum; empty, oversized, and unsupported objects return bounded reasons;
- storage and extractor failures do not expose raw SDK or extraction details;
- transactional re-read rejects changed session status/expiry/token, intent storage
  key/expiry/status, and changed or missing Personal Details;
- an injected failure at the final event write rolls back earlier intent, artifact,
  and session-state writes; and
- neither mismatch errors nor event metadata contain submitted or extracted identity
  numbers.

Verification evidence on 2026-07-22:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -count=1
28 tests passed

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
28 tests passed
```

Replay of `confirmed`/`validation_failed` outcomes and concurrent-confirm coordination
remain intentionally deferred to Checkpoint 6.

## Checkpoint 4: strict HTTP path and PostgreSQL tracer

Checkpoint 4 is complete. The user-authored confirm success and unknown-field cycles
remain intact. Sibling tests now freeze strict request framing, one MiB request
limits, UUID/auth/method validation, exact success DTOs, and bounded application
errors for both confirm and upload-url.

The HTTP adapter owns two narrow consumer interfaces: one for confirmation and one
for creating Upload Intents. This keeps object storage, extraction, PostgreSQL, and
transaction types outside HTTP contracts. Artifact routes are registered only when
their dependency is non-nil, so `cmd/api` can compile with the routes deliberately
disabled until the real MinIO boundary exists.

The integration tracer calls upload-url through the public handler, configures fake
object metadata and extraction by the exact presigned storage key, then confirms
using the returned Upload Intent ID through the same public handler. Both application
services and the PostgreSQL adapter are real. Missing fake storage/extraction entries
return explicit errors rather than zero values.

Verification evidence on 2026-07-28:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/adapter/httpapi ./cmd/api -count=1
98 tests passed

GOCACHE=/tmp/lawang-go-build go test ./tests/integration -run 'HTTP|Artifact' -count=1
7 tests passed

GOCACHE=/tmp/lawang-go-build go test -race ./internal/adapter/httpapi -count=1
98 tests passed

GOCACHE=/tmp/lawang-go-build go test -race ./tests/integration \
  -run '^TestIdentityDocumentUploadAndConfirmOverHTTPWithPostgreSQL$' -count=1
1 test passed
```

Usable public-host presigning and real MinIO `HeadObject` remain intentionally
deferred to Checkpoint 5. Replay and concurrent-confirm coordination remain
Checkpoint 6 work.

## Later checkpoints

- Public-host presigning and `HeadObject` through the MinIO boundary.
- Replay and concurrent-confirm coordination without repeated external work.
