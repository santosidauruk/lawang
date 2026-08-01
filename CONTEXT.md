# Lawang Go Project Context

## Purpose

Lawang Go is an independent Go implementation of the Lawang Verification bounded
context. It reproduces the observable TypeScript behavior delivered through the old
project's issues 1-6, then extends the service through direct uploads, deterministic
local validation, asynchronous provider submission, signed webhooks, expiry,
cleanup, and audit capabilities.

The project is learning-first. Handwritten SQL, native `net/http`, explicit
transactions, and visible failure handling are part of the product contract, not
temporary scaffolding.

## Source of Truth

Use sources in this order:

1. `docs/plan-go.md` for the approved implementation, architecture, security, and
   collaboration contract.
2. `CONTEXT.md` for stable domain language and cross-cutting invariants.
3. `docs/adr/` for accepted architectural decisions once those records exist.
4. `docs/issues/` for the ordered implementation contracts.
5. Tests and request/response evidence from the old TypeScript Lawang repository for
   compatibility details through Issue 006.

The old TypeScript repository is evidence only. Never import its runtime code, copy
its migration history, share its database, or couple deployment lifecycles.

When sources contradict one another, stop and surface the conflict. Do not silently
tighten the public API or infer a new domain rule.

## Fixed Boundaries

- Go baseline and minimum required version: Go 1.26.5, the latest stable release
  verified when the project baseline was upgraded on 2026-07-28.
- Module path: `github.com/santosidauruk/lawang-go`, explicitly approved by the user
  for this independent repository.
- Local development database:
  `postgresql://lawang:lawang@localhost:5432/lawang_db_go`.
- Tests use disposable PostgreSQL, MinIO, and Redis instances. They never mutate
  `lawang_db_go`.
- No frontend, production deployment, ORM, query builder, real OCR, real biometric
  recognition, or real verification provider through Issue 010.
- No production-readiness or regulatory-compliance claims.

## Bounded Context and Glossary

The system has one bounded context: **Verification**.

| Term | Meaning |
| --- | --- |
| Applicant | Person undergoing verification. |
| Verification Session | Time-limited aggregate owning one verification attempt. |
| Personal Details | Immutable applicant identity data submitted once per session. |
| Identity Document | Document evidence such as an identity card. |
| Biometric Capture | Selfie or other biometric evidence. |
| Upload Intent | Temporary authorization and storage key for one direct upload attempt; it is not evidence. |
| Stored object | Bytes present in S3-compatible storage; presence alone does not make them accepted evidence. |
| Verification Artifact | Accepted evidence recorded only after storage checks and required local validation succeed. |
| Local Validation Failure | Deterministic rejection before provider submission. |
| Verification Provider | External system that evaluates accepted evidence. |
| Provider Submission | Asynchronous request sent to the provider. |
| Verification Verdict | Provider outcome: verified or rejected with a bounded reason. |
| Webhook Event | Signed provider callback deduplicated by provider event ID. |
| Session Event | Append-only normalized audit event whose bounded type names the action applied to a Verification Session. |
| Expired Session | Terminal Verification Session that exceeded its applicable deadline. |
| Implementation evidence | Test, SQL proof, or command output retained for project handoff; never call this a Verification Artifact. |

Use the full term **Verification Artifact** when referring to accepted evidence.
Keep Upload Intent, stored object, and Verification Artifact distinct everywhere.

## Public Session States

Exact public strings:

```text
created
personal_details_submitted
identity_document_uploaded
biometric_capture_uploaded
verification_pending
verified
rejected
expired
```

Required forward path:

```text
created
  -> personal_details_submitted
  -> identity_document_uploaded
  -> biometric_capture_uploaded
  -> verification_pending
  -> verified | rejected
```

`expired` may be entered from any non-terminal state after the applicable deadline.
`verified`, `rejected`, and `expired` are terminal. Biometric-before-document is not
supported by this contract.

## Session Event Action Verbs

Session Event types are action-based and use exactly these database and Go strings:

```text
submit_personal_details
confirm_identity_document
confirm_biometric_capture
submit_session
verification_passed
verification_failed
expire
```

Do not derive Session Event types from public state names. Public states describe the
resulting aggregate state; Session Event types describe the action that was applied.
The canonical successful transition mapping is:

| Session Event type | Resulting public state |
| --- | --- |
| `submit_personal_details` | `personal_details_submitted` |
| `confirm_identity_document` | `identity_document_uploaded` |
| `confirm_biometric_capture` | `biometric_capture_uploaded` |
| `submit_session` | `verification_pending` |
| `verification_passed` | `verified` |
| `verification_failed` | `rejected` |
| `expire` | `expired` |

`confirm_identity_document` is also recorded when the confirmation action completes
with a bounded Local Validation Failure and the session state does not advance. Put
the safe bounded outcome in event metadata; do not create another result-based event
type. Idempotent replays and ignored provider callbacks do not append another Session
Event.

## Aggregate Invariants

- Resume tokens are high-entropy opaque credentials. Store only deterministic
  cryptographic hashes and return the raw token once.
- An applicant-facing session expires 30 minutes after creation. The application
  clock decides both creation expiry and resume eligibility; `now >= expires_at` is
  expired.
- Personal Details is immutable and one-to-one with a Verification Session.
- Identical Personal Details replay succeeds without another write or event;
  different replay returns a conflict.
- At most one pending Upload Intent exists per session and evidence kind.
- A replacement intent supersedes the older pending intent and uses a new key.
- Upload constraints are code-owned by bounded evidence kind and are not snapshotted
  into Upload Intent rows. Confirmation uses the currently deployed kind mapping.
- The application generates each Upload Intent ID and derives a fresh storage key
  before persistence. Identity Document keys use
  `verification-sessions/{sessionID}/identity_document/{intentID}`. Presigning
  happens outside the database transaction; only after signing succeeds does a
  short guarded transaction supersede the previous pending intent and insert the
  new one.
- One Upload Intent creates at most one Verification Artifact.
- A Verification Artifact exists only after metadata checks and required local
  validation succeed.
- Provider submission requires accepted Identity Document and Biometric Capture
  Verification Artifacts.
- Every state transition and Session Event commit atomically.
- The database constraint and Go event type/constants admit exactly the seven Session
  Event action verbs above; neither layer accepts arbitrary strings.
- External network and object-storage I/O never runs inside a database transaction.
- After external I/O, a short transaction re-reads and guards relevant state before
  committing.
- Terminal states never transition.

## Public HTTP Contract

Compatibility includes method, route, status, JSON field names, public state strings,
error envelope, authentication, replay, and conflict behavior.

Expected errors use:

```json
{
  "code": "MACHINE_READABLE_CODE",
  "message": "Human-readable summary",
  "details": {}
}
```

Do not expose secrets, identity numbers, addresses, raw extraction output, object
URLs, webhook bodies, stack traces, or SDK/database errors.

Applicant routes use a resume token in `Authorization: Bearer <token>`. The Bearer
scheme is case-insensitive; the credential must be exactly one non-whitespace value.
The provider webhook uses HMAC-SHA256 and does not use the resume token.

Issue 002 freezes `401 MISSING_AUTHORIZATION`, `401 MALFORMED_AUTHORIZATION`, and
`401 INVALID_RESUME_TOKEN`. Legacy compatibility distinguishes an unknown UUID as
`404 SESSION_NOT_FOUND`; expiry added by the Go contract is `410 SESSION_EXPIRED`.

## Architecture and Dependency Rules

Dependency direction:

```text
cmd composition -> adapters -> application -> domain
```

- Domain code imports only the standard library and explicitly accepted value-type
  libraries such as `google/uuid`.
- Application code owns use cases and transaction boundaries and defines ports it
  consumes.
- Adapters implement HTTP, PostgreSQL, object storage, extraction, provider, and
  queue concerns without leaking their types inward.
- Only `cmd/*` wires concrete implementations.
- Define interfaces at the consumer only for real substitutable boundaries.
- Do not add generic repositories, base services, dependency-injection frameworks,
  or DTO/mapper/interface layers by template.
- `context.Context` is the first argument of I/O methods and is never stored.

## Chosen Technology

- HTTP: standard library `net/http`.
- PostgreSQL: `pgx/v5` with `pgxpool`.
- Typed queries: sqlc; handwritten SQL remains authoritative.
- Migrations: Goose, sequential Up-only SQL, forward repair only.
- Validation: strict JSON decoder, adapter-level validator, and explicit semantic
  parsing.
- Object storage: MinIO locally through AWS SDK for Go v2.
- Async jobs: Redis and pinned Asynq behind an adapter.
- Reliability: PostgreSQL transactional outbox.
- Logging: JSON `log/slog` with sensitive-data minimization.
- Integration dependencies: Testcontainers for Go.

## SQL and Migration Discipline

For every database change: state the invariant, write a forward migration, prove
relational behavior with disposable PostgreSQL and `psql`, add named sqlc queries,
inspect generated types, wrap them in the PostgreSQL adapter, then test the use case.

Never run migrations automatically at API startup. Never offer routine down/reset
workflows. Serialize Goose execution externally. Generated sqlc files are committed,
never hand-edited, and CI must fail if regeneration leaves a diff.

## Security and Data Minimization

- Never log tokens, Authorization headers, presigned URLs, object contents, raw
  extraction output, identity numbers, addresses, or webhook bodies.
- Session Event and outbox payloads contain identifiers and safe bounded outcomes,
  not Personal Details or object contents.
- Validate body sizes, unknown JSON fields, multiple JSON values, server timeouts,
  object metadata, and configuration at startup.
- Verify webhook signatures over exact raw bytes with `hmac.Equal`; invalid bodies
  are neither parsed nor stored.
- Local plaintext PII is an explicit MVP limitation.

## Testing and Completion

Use table-driven domain tests, application tests with small fakes, `httptest`
contract tests, real disposable PostgreSQL/MinIO/Redis integration tests, and a full
API-worker-provider end-to-end suite. Fixed clocks replace timing sleeps.

An issue is complete only when observable behavior, database invariants, failure
behavior, and relevant replay/concurrency obligations are proven. Keep exact commands
and outputs as implementation evidence and add a concise matching file under
`docs/learning/`.

## Collaboration Rules

- Issues 001-006 may be implemented autonomously after their prerequisites are
  available. Stop for contradictions that would change compatibility.
- Issues 007-010 are HITL learning slices. The user writes the first concept-bearing
  migration, proof, test, or adapter operation named by the issue. Review that work
  before implementing sibling or repetitive cases.
- Do not advance past Issue 006 until the parity report and current quality gate are
  green.
- Implement issues in dependency order from `docs/issues/README.md`.
