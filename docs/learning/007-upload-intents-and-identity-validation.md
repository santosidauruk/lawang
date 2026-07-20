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

- Next: the first user-authored successful-confirmation test through the application
  public interface.
- PostgreSQL transaction and adapter boundary.
- Strict HTTP decoding, handler, route registration, and runtime wiring.
- Public-host presigning and `HeadObject` through the MinIO boundary.
- Replay and concurrent-confirm coordination without repeated external work.
