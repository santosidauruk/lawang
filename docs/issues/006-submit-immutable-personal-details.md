# Submit Immutable Personal Details

Status: done
Type: AFK
Labels: done
Source: `docs/plan-go.md` sections 4-8, 12, 14, 16 Issue 6, and 17.1

## User stories covered

- US-04: Submit immutable Personal Details with safe replay and conflict behavior.

## What to build

Add the first authenticated state-changing applicant command end to end. Before
implementation, inspect the old TypeScript Issue 006 request/response tests and
record the exact field names, presence-versus-empty semantics, statuses, replay
behavior, and error envelopes in this issue or a linked compatibility fixture. Then
implement immutable Personal Details persistence, strict-but-compatible HTTP
validation, and one atomic insert/state/event transaction.

The verified legacy contract is frozen in
[`docs/compatibility/006-legacy-personal-details.md`](../compatibility/006-legacy-personal-details.md).

## Scope boundaries

- Preserve observable TypeScript behavior; do not tighten validation based only on
  Go validator conventions.
- Do not add upload, object-storage, or provider behavior.
- Do not duplicate Personal Details or PII in Session Events or logs.
- Do not mutate the old TypeScript repository while extracting evidence.

## Domain and data invariants

- Personal Details is one-to-one with a Verification Session and immutable.
- First valid submission is accepted only from `created`.
- Identical replay returns success without another write or event.
- Different replay returns `409 Conflict` without mutation.
- Insert, transition to `personal_details_submitted`, and one safe Session Event commit
  atomically.
- The first accepted submission appends `submit_personal_details`; identical replay
  appends no event.
- Concurrent different submissions cannot both succeed.

## Acceptance criteria

- [x] Exact legacy request fields, response shape, validation edge cases, and error
      codes are captured from actual old tests before code is written.
- [x] A forward migration creates a one-to-one Personal Details record with suitable
      date/text types and no redundant session summary fields.
- [x] `POST /verification-sessions/{id}/personal-details` requires a valid resume
      token and returns the current session summary on success.
- [x] First valid submission writes details, guarded state transition, and exactly one
      `submit_personal_details` Session Event in one transaction.
- [x] Identical replay returns `200` and performs no write/event; different replay
      returns the exact legacy `409` envelope.
- [x] Wrong state, terminal state, wrong token, malformed JSON, unknown fields,
      expiry, and validation failures match frozen compatibility tests.
- [x] Concurrent identical submissions converge idempotently; concurrent different
      submissions produce one winner and one conflict.
- [x] Events, logs, and errors contain no full Personal Details or identity values.
- [x] Issue 001-006 route-by-route compatibility report is generated and all current
      quality, SQL, and parity checks pass before Issue 007 starts.

## API example

```http
POST /verification-sessions/{id}/personal-details HTTP/1.1
Authorization: Bearer <resume-token>
Content-Type: application/json

{
  "fullName": "Alice Applicant",
  "dateOfBirth": "1990-01-02",
  "identityNumber": "1234567890",
  "address": "Jalan Perjuangan 1"
}
```

The response is the same public session summary shape used by the authorized GET
route: `{ "id", "status", "expiresAt" }`.

## SQL proof

Against disposable PostgreSQL, prove:

1. one Personal Details row per Verification Session;
2. insert + expected-state update + event commit atomically;
3. forced failure rolls back all three writes;
4. two concurrent different inserts cannot both commit;
5. reads needed for replay comparison do not expose data outside the authorized path.

## Tests

- DTO/validation contract cases copied from verified legacy behavior, including
  presence versus empty-string semantics.
- Application tests for first submit, identical replay, conflicting replay, wrong
  state, expiry, and rollback.
- HTTP auth/decoding/error snapshot tests.
- Real PostgreSQL integration tests for atomicity and concurrent submissions.
- Full Issue 001-006 compatibility suite.

## Verification commands

Run the complete current quality gate, disposable PostgreSQL SQL proofs/integration
tests, and the compatibility suite. Produce a route-by-route report showing legacy
evidence and Go result for method, status, fields, auth, replay, and conflict cases.

## Implementation evidence and learning note

Retain the legacy test references, compatibility report, SQL concurrency proof,
request/response fixtures, and full gate output. Add
`docs/learning/006-immutable-personal-details.md` explaining one-to-one data,
idempotent replay, conflicts, and atomic state/event writes.

Completed in [`docs/learning/006-immutable-personal-details.md`](../learning/006-immutable-personal-details.md).
The legacy fixture is retained in
[`docs/compatibility/006-legacy-personal-details.md`](../compatibility/006-legacy-personal-details.md),
and the Issue 001-006 route report is retained in
[`docs/compatibility/issues-001-006-report.md`](../compatibility/issues-001-006-report.md).
The disposable PostgreSQL suite proves the one-to-one schema, three-write commit and
rollback, safe event metadata, HTTP path, and identical/different concurrent
submissions using separate connections.

## Blocked by

- [005 - Harden the session HTTP-to-SQL path](./005-harden-session-http-to-sql-path.md)
- Read-only access to the old TypeScript Issue 006 behavioral tests. The sibling
  `../lawang` repository exists in the current environment, but its exact role/path
  must be revalidated at implementation time.
