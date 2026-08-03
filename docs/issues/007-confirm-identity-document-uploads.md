# Confirm Identity Document Uploads

Status: ready-for-human
Type: HITL
Labels: ready-for-human
Source: `docs/plan-go.md` sections 4-5, 6.4, 8-9, 12, 14, 16 Issue 7, and 17.2

## User stories covered

- US-05: Upload and confirm an Identity Document with deterministic local-validation
  results.

## What to build

Deliver direct Identity Document upload as one complete path: create/supersede an
Upload Intent, return a public-host-presigned MinIO URL, inspect the uploaded object's
actual metadata, run deterministic fake extraction, reconcile it with immutable
Personal Details, then atomically record either a bounded Local Validation Failure or
an accepted Verification Artifact and state/event transition.

## Required learning checkpoint

Before agent implementation, the user writes:

1. the first concept-bearing Upload Intent migration and its
   one-pending-intent-per-kind concurrency/constraint proof; and
2. the first application test describing successful confirmation.

The agent reviews correctness, PostgreSQL concurrency semantics, and the distinction
between Upload Intent, stored object, and Verification Artifact. The agent may then
implement repetitive queries, adapter operations, and sibling tests. Do not silently
replace a concept-bearing mistake; ask the user to revise it.

## Active collaboration checkpoint

Checkpoint 1 is complete and reviewed:

1. `sql/migrations/00005_create_upload_intents.sql`;
2. `sql/proofs/005_upload_intent_constraints.sql`; and
3. the first real concurrent-creation test in
   `tests/integration/upload_intents_postgres_test.go`.

The migration proof and the real two-connection PostgreSQL test prove the
one-pending-intent invariant. Checkpoints 2-4 are GREEN, including the application,
PostgreSQL, strict HTTP, HTTP + PostgreSQL, and Checkpoint 5 MinIO paths are GREEN.
The public route now proves upload-url -> direct HTTP PUT -> real `HeadObject` ->
deterministic extraction -> atomic PostgreSQL confirm. Replay/concurrency hardening
remains Checkpoint 6.

## Scope boundaries

- Identity Document only; Biometric Capture belongs to Issue 008.
- Use MinIO through AWS SDK for Go v2; SDK types stay behind `ObjectStorage`.
- The extractor is deterministic test logic, not OCR.
- Do not perform S3 calls/extraction inside a database transaction.
- Do not automatically delete accepted Verification Artifacts.

## Domain and storage invariants

- One pending intent per `(session_id, identity_document)`; a new intent atomically
  supersedes the prior pending intent and uses a unique key.
- The application generates the intent UUID and a fresh storage key before
  persistence. Identity Document uses
  `verification-sessions/{sessionID}/identity_document/{intentID}`. Presign outside
  a transaction, then transactionally re-read guards, supersede the previous
  pending intent, and insert the new one.
- File constraints are derived from the bounded kind in application code and are not
  copied into each intent row. Confirmation uses the currently deployed mapping.
- If presigning fails, do not create or supersede an intent. If the transactional
  re-read is stale, discard the unreturned URL and commit no stale intent.
- Presigned URL and intent TTL are five minutes; URL signing uses the public endpoint
  without host rewriting.
- Confirm trusts `HeadObject`, not request metadata: JPEG/PNG/PDF, non-zero, <= 10 MiB.
- A confirmed intent creates at most one Verification Artifact.
- Local mismatch records `confirm_identity_document` with bounded failure metadata,
  creates no artifact, and does not advance state.
- Confirmed and validation-failed replay returns the recorded outcome without another
  S3/extractor call or event.
- External I/O results are discarded when the transactional re-read detects stale
  state.

## Acceptance criteria

- [x] User-authored migration/proof passes critical review before sibling work begins.
- [x] `POST /verification-sessions/{id}/artifacts/upload-url` accepts only
      `identity_document` for this slice and returns a new intent ID and usable
      public-host presigned URL.
- [x] A new intent atomically supersedes the old pending intent; concurrency still
      leaves exactly one pending intent and unique storage keys.
- [x] Real MinIO upload plus confirm enforces expiry, supersession, actual content
      type, non-zero size, and 10 MiB maximum.
- [x] Deterministic extraction proves success and at least
      `identity_number_mismatch` without storing raw extraction output.
- [x] Successful confirm creates exactly one accepted Verification Artifact, advances
      to `identity_document_uploaded`, and appends one safe
      `confirm_identity_document` event atomically.
- [x] Mismatch returns exact `422 LOCAL_VALIDATION_FAILED`, records bounded failure,
      appends one safe `confirm_identity_document` event with bounded outcome metadata,
      creates no artifact, and leaves session state unchanged.
- [ ] Confirmed/failed replay and concurrent confirms are idempotent and do not repeat
      external work or database effects.
- [x] SDK/storage errors map to safe bounded application/HTTP failures.

## API examples

```http
POST /verification-sessions/{id}/artifacts/upload-url HTTP/1.1
Authorization: Bearer <resume-token>
Content-Type: application/json

{"kind":"identity_document"}
```

```http
POST /verification-sessions/{id}/artifacts/confirm HTTP/1.1
Authorization: Bearer <resume-token>
Content-Type: application/json

{"uploadIntentId":"00000000-0000-0000-0000-000000000000"}
```

Mismatch response:

```json
{
  "code": "LOCAL_VALIDATION_FAILED",
  "message": "The uploaded identity document did not match the submitted details",
  "details": {"reason":"identity_number_mismatch"}
}
```

Freeze the success response body before implementation; it should be the current
session summary unless an explicitly reviewed contract says otherwise.

## SQL proof

The user-authored proof must demonstrate the partial unique constraint under
concurrent intent creation. Additional proofs cover supersession, unique intent/key,
one artifact per intent and per `(session, kind)`, and atomic failure/success effects.

## Tests

- User-authored successful-confirm application test, then sibling failure/replay
  cases with small storage/extractor fakes.
- HTTP contract tests for kinds, UUIDs, auth, errors, and body decoding.
- Disposable PostgreSQL concurrency tests.
- Real MinIO presigned upload, public-host signature, and `HeadObject` tests.
- Race tests for intent creation and confirm.

## Verification commands

Run the full quality gate plus explicit disposable PostgreSQL and MinIO integration
commands. Verify no test uses `lawang_db_go` and no test silently skips Docker.

## Implementation evidence and learning note

Retain reviewed user-authored files, SQL concurrency output, real MinIO upload proof,
all replay/race test output, and safe error examples. Add
`docs/learning/007-upload-intents-and-identity-validation.md`.

## Blocked by

- [006 - Submit immutable Personal Details](./006-submit-immutable-personal-details.md)
- Green Issue 001-006 compatibility report and current quality gate.
- Required user-authored checkpoint described above.
