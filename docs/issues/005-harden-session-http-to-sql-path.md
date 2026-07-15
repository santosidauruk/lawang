# Harden the Session HTTP-to-SQL Path

Status: needs-triage  
Type: AFK  
Labels: needs-triage  
Source: `docs/plan-go.md` sections 5, 6, 7, 13-16, and 16 Issue 5

## User stories covered

- US-02: Reliably create and resume a Verification Session.
- US-03: Trust clean boundaries, cancellation, safe errors, and production-shaped HTTP
  behavior before adding more commands.

## What to build

Refactor and harden the already working create/resume path into the target pragmatic
Clean Architecture: complete `net/http` adapter, focused decoding/auth/error helpers,
application use cases, sqlc-backed PostgreSQL adapter, and `cmd/api` composition. The
observable Issue 001-002 behavior must not change.

This is an architectural tracer/proof, not a new product feature. It exists separately
because the approved learning plan requires final boundary discipline before Personal
Details adds a transactional command.

## Scope boundaries

- Do not create an internal HTTP framework, generic handler DSL, generic repository,
  base use case, dependency-injection container, or interface per struct.
- Do not expose sqlc/pgx/HTTP DTO/AWS/Asynq types to application or domain packages.
- Do not add Issue 006 request fields before extracting the legacy contract.

## Architecture and HTTP invariants

- Dependency direction is `cmd -> adapters -> application -> domain`.
- I/O-bound methods take `context.Context` first and never store it.
- HTTP servers set read/write/idle timeouts and propagate cancellation.
- JSON helpers limit bodies, reject unknown fields and multiple values, validate at
  the adapter boundary, and map expected failures to the public error envelope.
- Application use cases own transactions; external I/O never happens inside them.

## Acceptance criteria

- [ ] `cmd/api` is the only package wiring concrete HTTP/PostgreSQL/platform types.
- [ ] Domain and application packages compile without importing adapter libraries.
- [ ] Request ID, method, route, response status, duration, and safe error code are
      logged without secrets or Authorization headers.
- [ ] JSON endpoints use size limits, `DisallowUnknownFields`, exactly-one-value
      decoding, and explicit DTO conversion.
- [ ] Method, malformed input, auth, not-found/conflict/expiry, and internal failures
      map to frozen public statuses and envelopes.
- [ ] Context cancellation reaches PostgreSQL calls and server shutdown is graceful.
- [ ] Issue 001-002 HTTP/PostgreSQL integration coverage remains green without public
      contract drift.
- [ ] The package layout stays cohesive and contains only interfaces justified by a
      consumed boundary.

## API examples

Reuse the exact create/resume examples and error fixtures frozen in Issue 002. Add
negative contract examples for unknown JSON fields and multiple JSON values only on
routes that accept JSON; do not invent a body for session creation if compatibility
defines none.

## SQL proof

No new schema invariant is required. Regenerate sqlc, inspect the generated signatures,
and prove the adapter preserves database cancellation/error classification without
leaking driver details into the public response.

## Tests

- `httptest` tables for routing, method handling, body limits/decoding where
  applicable, Bearer auth, and error mapping.
- Application tests use small consumer-owned fakes.
- PostgreSQL adapter integration tests use real migrations and cancellation.
- Dependency-direction check and existing API snapshot suite.

## Verification commands

Run the full current quality gate, focused HTTP/PostgreSQL integration suite, and a
dependency import search. `sqlc generate` must leave no generated diff.

## Implementation evidence and learning note

Retain package dependency evidence, cancellation test output, request/response
snapshots, and sqlc clean-diff output. Add
`docs/learning/005-http-application-postgres-boundaries.md` explaining native HTTP
ownership, adapter conversion, cancellation, and pragmatic interfaces.

## Blocked by

- [004 - Enforce the Verification Session state machine](./004-enforce-verification-session-state-machine.md)

