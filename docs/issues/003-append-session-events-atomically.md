# Append Session Events Atomically

Status: done  
Type: AFK  
Labels: done  
Source: `docs/plan-go.md` sections 4, 6.4, 8, 14, and 16 Issue 3

## User stories covered

- US-03: Trust that session changes are atomic and auditable.

## What to build

Add append-only Session Events and an application-owned transaction boundary, then
demonstrate one narrow state-plus-event transition atomically through the application
and PostgreSQL adapter. This is a learning/proof slice rather than a new public route;
its demo is a disposable-database transaction that either commits both records or
neither.

## Scope boundaries

- Do not implement the complete state machine; Issue 004 owns transition policy.
- Do not put Personal Details or sensitive data into event metadata.
- Do not expose an audit HTTP endpoint.
- Do not introduce generic unit-of-work or repository abstractions.

## Domain and data invariants

- `session_events` is append-only and uses exactly these action-based event types:
  `submit_personal_details`, `confirm_identity_document`,
  `confirm_biometric_capture`, `submit_session`, `verification_passed`,
  `verification_failed`, and `expire`.
- Public state/result strings such as `personal_details_submitted`, `verified`, and
  `expired` are not Session Event types.
- A state transition and its Session Event commit in one PostgreSQL transaction.
- The application use case owns transaction boundaries; the adapter supplies the
  mechanism.
- Domain/application packages import neither pgx, sqlc, nor `pgtype`.

## Acceptance criteria

- [x] A forward migration creates `session_events` with session FK, bounded event
      type, safe JSON metadata, and `timestamptz` occurrence time.
- [x] The database rejects every event type outside the exact seven-verb list, and
      the Go domain exposes the same strings as a typed event type/constants rather
      than accepting arbitrary strings.
- [x] Named sqlc queries append and read ordered Session Events.
- [x] An application-consumed transaction port and PostgreSQL implementation support
      a focused state-plus-event use case without leaking adapter types.
- [x] A forced failure after the guarded update proves both update and event roll back.
- [x] The success path commits exactly one update and exactly one event.
- [x] Event payload policy and tests reject or structurally prevent Personal Details,
      tokens, object URLs, identity numbers, addresses, and raw extraction/provider
      bodies.
- [x] Package dependency checks show adapters -> application -> domain.

## API examples

No public route is added. Existing Issue 002 request/response contracts must remain
unchanged. A regression test proves the refactor does not change them.

## SQL proof

Provide independently runnable `psql` proofs for:

1. successful session update plus event commit;
2. forced failure after update and before commit leaves both tables unchanged;
3. event ordering is deterministic by occurrence time plus stable event ID;
4. deleting or mutating events is not part of any application query surface.

The proof must exercise an allowed action verb and show an unknown verb plus a public
state/result string are rejected by the database constraint.

## Tests

- Application transaction tests with a small failure-injection fake.
- PostgreSQL integration test for commit and rollback behavior.
- Adapter boundary/compile tests or dependency checks preventing pgx/sqlc leakage.
- Existing create/resume contract regression tests.

## Verification commands

Run the full current quality gate, the raw SQL proof with `psql`, and the focused
PostgreSQL integration package against a disposable database.

## Implementation evidence and learning note

Retain SQL proof output, transaction failure test output, dependency evidence, and
the unchanged session API snapshot. Add
`docs/learning/003-atomic-session-events.md` explaining transaction ownership,
rollback, append-only audit data, and safe metadata.

Completed in `docs/learning/003-atomic-session-events.md`. The disposable PostgreSQL
proof reports atomic commit and rollback, deterministic event ordering, exact verb
and safe-metadata constraints, and append-only mutation rejection. Application,
adapter, architecture, generated-query, and unchanged create/resume HTTP regression
tests pass through the repository quality gate.

## Blocked by

- [002 - Create and resume a Verification Session](./002-create-and-resume-verification-session.md)
