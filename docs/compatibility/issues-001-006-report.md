# Issues 001-006 Compatibility Report

Verified on 2026-07-16 against the read-only sibling TypeScript repository
`../lawang` and the current Go implementation. Detailed Personal Details source and
runtime-probe evidence is retained in
[`006-legacy-personal-details.md`](./006-legacy-personal-details.md).

## Route report

| Route | Legacy evidence | Go result |
| --- | --- | --- |
| `GET /health/live` | TypeScript health contract retained by the rebuild plan | `200`, JSON `{ "status": "ok" }`; `TestLiveHealthContract` passes |
| `POST /verification-sessions` | TypeScript session route/schema tests | `201`; exact `id`, `status`, `expiresAt`, `resumeToken` fields; method and internal errors remain frozen by HTTP and PostgreSQL integration tests |
| `GET /verification-sessions/{id}` | TypeScript session route/schema/service tests | `200`; exact `id`, `status`, `expiresAt`; case-insensitive Bearer auth; missing, malformed, wrong-token, not-found, bad-id, and Go expiry envelopes pass |
| `POST /verification-sessions/{id}/personal-details` | TypeScript Personal Details schema, route, service, repository tests, plus read-only `app.inject` probes | `200`; exact `id`, `status`, `expiresAt`; strict request, auth, replay, conflict, state, expiry, malformed JSON, concurrency, and redaction tests pass |

Issues 003-005 add no public routes. Their guarded state transition, append-only
Session Event, transaction, cancellation, logging, and package-boundary behavior
remain covered by the existing domain, application, HTTP, architecture, and
PostgreSQL suites.

## Personal Details compatibility matrix

| Behavior | Legacy | Go |
| --- | --- | --- |
| method/path | `POST /verification-sessions/:id/personal-details` | exact equivalent with `{id}` path syntax |
| request fields | `fullName`, `dateOfBirth`, `identityNumber`, `address` | exact |
| required versus empty | all present; empty strings accepted for the three text fields | exact; pointer DTO distinguishes missing from empty |
| date | valid `YYYY-MM-DD` calendar date | exact, parsed to a date value before application use |
| unknown/missing/null/type/date error | `400 VALIDATION_ERROR` | same status/code and safe API envelope |
| malformed, empty, or multiple JSON values | `500 INTERNAL`, `Internal server error` | exact |
| first submit | `200`, state `personal_details_submitted` | exact; details/state/event commit atomically |
| identical replay | `200`, current summary, no duplicate event | exact, including concurrent identical requests |
| different replay | `409 PERSONAL_DETAILS_CONFLICT` with session ID | exact, including one-winner/one-conflict concurrency |
| wrong state or terminal state | `409 ILLEGAL_TRANSITION` with `from` and `event` | exact; Go envelope also retains the route session ID |
| missing/malformed/wrong credential | `401` with stable code | exact |
| unknown session | `404 SESSION_NOT_FOUND` | exact |
| expired session | absent from legacy Issue 006 | Go extension remains `410 SESSION_EXPIRED` |
| event payload | old implementation wrote request data into event metadata | intentionally superseded by the approved Go security contract: empty metadata, no PII |

The last row is a documented contract difference, not accidental drift. The current
Go source of truth explicitly forbids Personal Details in events and logs and requires
the action verb `submit_personal_details`.

## Verification result

The following current gates pass:

- `make test` (`go test -race ./...`), including disposable PostgreSQL and real
  two-connection concurrency tests;
- `make fmt-check vet staticcheck sqlc-diff migration-validate compose-validate`;
- `sql/proofs/004_immutable_personal_details.sql` through the disposable PostgreSQL
  integration suite;
- read-only legacy runtime probes for unknown, missing, null, wrong-type, invalid
  date, empty text, malformed, empty-body, and multiple-JSON cases.

