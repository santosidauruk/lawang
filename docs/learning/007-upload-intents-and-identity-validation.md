# Upload Intents and Identity Validation

This is the working learning record for Issue 007. Record conclusions only after
the corresponding RED-GREEN checkpoint has been exercised and reviewed.

## Checkpoint 1: Upload Intent schema and concurrency

Frozen design decisions before schema completion:

- Upload constraints are code-owned: the row stores bounded `kind`, while Go maps
  `identity_document` to JPEG/PNG/PDF, non-zero, and at most 10 MiB. Pending intents
  use the currently deployed mapping rather than a snapshotted rule set.
- The application generates the Upload Intent UUID and uses it to construct a fresh
  storage key before persistence. It presigns outside a transaction. Only after
  signing succeeds does a short transaction re-read guards, supersede the previous
  pending intent, and insert the new row. Signing failure writes nothing; a stale
  transactional re-read discards the unreturned URL.

Pending user work:

- explain why an Upload Intent is neither a stored object nor a Verification
  Artifact;
- state the invariant enforced by migration `00005`;
- retain the SQL constraint proof result;
- retain the competing-connection test result; and
- explain the observed PostgreSQL behavior when two creations race.

## Later checkpoints

- Successful confirmation through the application public interface.
- PostgreSQL transaction and adapter boundary.
- Strict HTTP decoding, handler, route registration, and runtime wiring.
- Public-host presigning and `HeadObject` through the MinIO boundary.
- Replay and concurrent-confirm coordination without repeated external work.
