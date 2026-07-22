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

## Later checkpoints

- Checkpoint 2 is active: the first user-authored successful-confirmation test through
  the application public interface.
- PostgreSQL transaction and adapter boundary.
- Strict HTTP decoding, handler, route registration, and runtime wiring.
- Public-host presigning and `HeadObject` through the MinIO boundary.
- Replay and concurrent-confirm coordination without repeated external work.

## Checkpoint 2 guide: successful Identity Document confirmation

This checkpoint is one TDD tracer bullet, not a batch of imagined tests. The public
behavior is already frozen by `docs/plan-go.md`: confirmation receives the
Verification Session ID, raw resume token, and Upload Intent ID, then returns the
current `session.Summary`. On success its status is `identity_document_uploaded`.

### File 1 — `internal/application/artifact/service_test.go` (user writes now)

1. Remove `t.Skip` only when you are ready to make the test RED.
2. Translate each ARRANGE comment into concrete values, starting with fixed IDs/time.
3. Write only the success scenario. Do not add mismatch, expiry, replay, concurrency,
   PostgreSQL, HTTP, or MinIO cases yet.
4. Add small handwritten fakes below the test. Fake only the system boundaries:
   transaction state, object storage, extractor, token hashing, and clock.
5. Call only the public `Confirm` method. Do not call future private helpers.
6. Assert the returned summary and committed state listed in the scaffold.
7. Run:

   ```sh
   go test ./internal/application/artifact -run \
     '^TestConfirmIdentityDocumentAcceptsValidatedUploadAtomically$' -count=1
   ```

   The expected RED may initially be a compile error because the public service does
   not exist. That is valid RED: retain the exact output for review.

Stop after this RED result and request review. Do not create `service.go` merely to
silence every compiler error at once; the reviewed test will determine its minimum
public types and consumed ports.

### File 2 — `internal/application/artifact/service.go` (after RED review)

The user will add the public input/result and minimal consumed interfaces revealed by
the test, then implement only enough successful-confirm behavior to make the single
test GREEN. The application use case owns the short transaction, but `HeadObject` and
document extraction must execute before it. The transaction must re-read the guarded
state before committing.

### Later files — not part of the current RED

After the first application test is reviewed and green, work proceeds vertically:

1. sibling application tests and minimal service behavior;
2. Upload Intent/Verification Artifact SQL queries and PostgreSQL transaction adapter;
3. strict HTTP decode, handler, route registration, and runtime wiring;
4. AWS SDK v2/MinIO implementation of the narrow ObjectStorage port;
5. replay and concurrent-confirm hardening.

Each step begins with one observable failing test. Do not pre-create all ports,
repository methods, DTOs, or adapters during Checkpoint 2.
