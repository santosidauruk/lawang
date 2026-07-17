# Issue 006 Legacy Personal Details Contract

This file freezes the observable TypeScript Issue 006 behavior before the Go
implementation is written. The sibling `../lawang` repository was inspected
read-only on 2026-07-16. Its runtime was also probed through Fastify `app.inject`
for validation cases that its checked-in tests do not assert exhaustively.

## Evidence

- `../lawang/src/modules/personal-details/personal-details.schemas.ts` defines the
  strict request and session-summary response schemas.
- `../lawang/tests/integration/personal-details.routes.test.ts` freezes the route,
  success, authentication, replay, conflict, unknown-field, missing-field, and
  wrong-state behavior.
- `../lawang/src/modules/personal-details/personal-details.service.ts` compares all
  four stored values for replay and owns the first-submit transaction.
- `../lawang/src/plugins/api-error.ts`, `../lawang/src/plugins/error-handler.ts`,
  and `../lawang/src/helpers/parseBearer.ts` define expected public envelopes.
- Read-only `app.inject` probes captured body-shape cases not asserted byte-for-byte
  by the old tests. No old source or database row was changed.

## Frozen request

`POST /verification-sessions/{id}/personal-details` requires
`Authorization: Bearer <resume-token>` and exactly these JSON properties:

| Property | Legacy schema | Presence and empty semantics |
| --- | --- | --- |
| `fullName` | string | required; `""` is accepted |
| `dateOfBirth` | ISO calendar date string | required; `""` is rejected; calendar-invalid dates are rejected |
| `identityNumber` | string | required; `""` is accepted |
| `address` | string | required; `""` is accepted |

The request object is strict. Unknown properties are rejected rather than stripped.
`null`, arrays, numbers for string fields, and missing properties are rejected.

## Frozen responses and failures

| Case | Status | Public result |
| --- | --- | --- |
| first valid submission from `created` | `200` | exactly `{ id, status, expiresAt }`, with status `personal_details_submitted` |
| identical replay | `200` | same current session summary; no second row or event |
| different replay | `409` | `PERSONAL_DETAILS_CONFLICT`, message `personal details already submitted with different values for session {id}`, details `{ id }` |
| valid payload in a non-`created` state without stored details | `409` | `ILLEGAL_TRANSITION`, message `illegal transition from {state} via submit_personal_details`, details `{ id, from, event }` |
| missing authorization | `401` | `MISSING_AUTHORIZATION`, message `Auth header required` |
| malformed authorization | `401` | `MALFORMED_AUTHORIZATION`, message `expected Bearer <token>` |
| wrong token | `401` | `INVALID_RESUME_TOKEN`, message `invalid resume token`, details `{ id }` |
| unknown session | `404` | `SESSION_NOT_FOUND`, message `session {id} not found`, details `{ id }` |
| unknown/missing/null/wrong-type field or invalid date | `400` | `VALIDATION_ERROR`; legacy `details` records `context` and validation issues |
| malformed JSON, empty body, or a second JSON value | `500` | `INTERNAL`, message `Internal server error` |

The checked-in legacy route tests assert the status and `VALIDATION_ERROR` code for
schema failures, not the complete Zod-generated issue payload. The Go compatibility
tests must preserve the status, code, envelope shape, field acceptance/rejection,
and safe messages without coupling to Zod's private issue format.

Expiry is a Go-contract extension absent from the old Issue 006 tests. The existing
Go session contract remains authoritative: an authorized submission at
`now >= expiresAt` returns `410 SESSION_EXPIRED` without mutation.

## Data and concurrency contract

- Personal Details is one-to-one with its Verification Session and has no update
  operation or `updated_at` column.
- `dateOfBirth` is stored as PostgreSQL `date`; the other values use required text
  columns. Empty text remains valid because compatibility permits it.
- The accepted event type is the Go contract's action verb
  `submit_personal_details`. Event metadata is empty and contains no Personal
  Details.
- Session locking plus a guarded transition must make concurrent submissions
  converge: identical requests both return success with one write/event; different
  requests produce one success and one conflict.

