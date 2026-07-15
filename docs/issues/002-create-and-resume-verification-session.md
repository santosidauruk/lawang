# Create and Resume a Verification Session

Status: done
Type: AFK  
Labels: done
Source: `docs/plan-go.md` sections 4, 5.1-5.2, 7, 8, 14, and 16 Issue 2

## User stories covered

- US-02: Create a Verification Session and securely resume it with a one-time-issued
  token.

## What to build

Deliver the first applicant-facing end-to-end path through HTTP, application logic,
handwritten SQL/sqlc, and PostgreSQL. `POST /verification-sessions` creates a session,
returns its raw resume token exactly once, and stores only the token hash. An
authorized `GET /verification-sessions/{id}` returns the current public summary.

## Scope boundaries

- Do not add Personal Details or Session Events yet.
- Do not add a generic auth framework or middleware DSL.
- Expiry behavior is enforced with an injected clock, but scheduled expiry belongs
  to Issue 010.

## Domain and security invariants

- Raw resume tokens are high-entropy opaque credentials and never enter DB or logs.
- Bearer scheme matching is case-insensitive; exactly one non-whitespace credential
  is accepted.
- Token hashing is deterministic for lookup and comparison is timing-safe where
  secrets are compared in process.
- Error responses use the public envelope and expose no storage/SDK/database detail.

## Acceptance criteria

- [x] `POST /verification-sessions` returns `201` with `id`, `created`, RFC3339
      `expiresAt`, and a raw `resumeToken`.
- [x] PostgreSQL remains the source of the session UUID and persisted default state.
- [x] Only a resume-token hash is persisted; the raw token is absent from DB, events,
      response logs, and error logs.
- [x] `GET /verification-sessions/{id}` requires a valid Bearer token and returns
      `200` without the raw token.
- [x] Missing, empty, multipart, whitespace-containing, malformed-scheme, wrong-token,
      unknown-session, and expired-session cases have frozen public responses.
- [x] Authorization cannot distinguish a session ID from a wrong token in a way that
      leaks sensitive existence unless the legacy contract explicitly requires it.
- [x] Fixed-clock application tests and real PostgreSQL/HTTP integration tests pass.

## API examples

```http
POST /verification-sessions HTTP/1.1
Host: localhost
Content-Length: 0

HTTP/1.1 201 Created
Content-Type: application/json

{
  "id": "00000000-0000-0000-0000-000000000000",
  "status": "created",
  "expiresAt": "2026-07-15T00:00:00Z",
  "resumeToken": "opaque-token-returned-once"
}
```

```http
GET /verification-sessions/00000000-0000-0000-0000-000000000000 HTTP/1.1
Authorization: Bearer opaque-token-returned-once

HTTP/1.1 200 OK
Content-Type: application/json

{
  "id": "00000000-0000-0000-0000-000000000000",
  "status": "created",
  "expiresAt": "2026-07-15T00:00:00Z"
}
```

Examples are structural; tests must use real generated IDs/tokens and exact legacy
error evidence.

## SQL proof

Prove token-hash uniqueness, authorized lookup by `(id, resume_token_hash)`, and that
no query returns the raw token. Use a disposable database and inspect the generated
sqlc signature for domain-friendly UUID/time types.

## Tests

- Token generator/hash unit tests with injected deterministic seams where necessary.
- Table-driven Bearer parser tests.
- Application tests for create, authorized resume, wrong credential, and fixed-clock
  expiry.
- `httptest` contract tests for status, JSON fields, headers, and error envelopes.
- Real PostgreSQL integration tests for persistence and lookup.

## Verification commands

Run the repository quality gate plus focused package/integration commands documented
by Issue 001. Capture a database query proving only the hash was stored.

## Implementation evidence and learning note

Retain request/response examples, a redacted DB proof, auth edge-case test output, and
the integration command. Add `docs/learning/002-resume-token-authentication.md`
explaining one-time token return, hashing, Bearer parsing, and fixed-clock expiry.

Completed in `docs/learning/002-resume-token-authentication.md`. The disposable
PostgreSQL proof reports a 32-byte stored hash without printing token material; the
focused application, HTTP contract, SQL, and real PostgreSQL/HTTP tests pass.

## Blocked by

- [001 - Scaffold an isolated runnable Go SQL lab](./001-scaffold-isolated-runnable-go-sql-lab.md)
