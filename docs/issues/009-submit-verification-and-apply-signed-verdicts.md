# Submit Verification and Apply Signed Verdicts

Status: ready-for-human
Type: HITL  
Labels: ready-for-human
Source: `docs/plan-go.md` sections 4-5, 6, 8, 10, 12-14, 16 Issue 9, and 17.2

Approved collaboration plan:
[`docs/plans/issue_009/README.md`](../plans/issue_009/README.md).
Active checkpoint:
[`Checkpoint 4 — Concurrent Outbox Relay`](../plans/issue_009/checkpoint-4-outbox-relay.md).

## User stories covered

- US-07: Submit verification asynchronously and receive an idempotently applied
  provider verdict.

## What to build

Deliver the complete asynchronous provider loop: atomically move an eligible session
to `verification_pending` with a Session Event and durable outbox row; relay work to
Asynq; submit to a separate deterministic fake provider with a stable idempotency key;
and authenticate, deduplicate, and state-guard signed webhook verdicts. Prove both
verified and rejected end-to-end outcomes and failure recovery.

## Required learning checkpoint

Before agent implementation, the user writes:

1. the first outbox migration/query and SQL proof of atomic
   session-event-outbox commit; and
2. the first webhook HMAC test over exact raw bytes.

The agent reviews transaction, crash-window, signature parsing, and constant-time
comparison risks before implementing the relay and sibling callback cases.

The approved checkpoint plan further prioritizes user-authored application code,
query/adapter operations, queue/provider operations, and runtime wiring. Repetitive
unit tests, fixtures, sibling errors, and direct-GREEN regressions belong to the
agent unless the proof technique itself is the learning concept.

## Scope boundaries

- One worker binary hosts relay and provider tasks; expiry/cleanup handlers arrive in
  Issue 010.
- Asynq stays behind `internal/adapter/queue` and is pinned exactly.
- The fake provider is a separate Go HTTP process; its scenario endpoint is test-only.
- Provider integration exhaustion is not an applicant rejection.
- No Kafka, Node worker, real provider, or synchronous provider wait in the API.

## Reliability and security invariants

- Submit requires both accepted artifacts and atomically commits pending state,
  one-day deadline, `submit_session` event, and outbox before returning `202`.
- An applied successful verdict appends `verification_passed`; an applied rejected
  verdict appends `verification_failed`.
- Redis/provider failure after commit cannot lose durable work.
- Outbox UUID is stable Asynq TaskID; session UUID is stable provider
  `Idempotency-Key`.
- Duplicate enqueue is treated as successful publication; concurrent relays claim
  safely.
- HMAC-SHA256 verifies exact raw bytes before parsing/logging/persistence.
- Invalid signatures return `401` and persist nothing.
- Valid duplicate or late/out-of-order callbacks return `200` without another state
  transition; late valid events are stored as bounded `ignored` outcomes.

## Acceptance criteria

- [ ] User-authored outbox proof and HMAC test pass critical review.
- [ ] `POST /verification-sessions/{id}/submit` requires both accepted artifacts,
      commits pending state, `submit_session`, and outbox atomically, and returns
      `202` without waiting for Redis/provider.
- [ ] Exact replay while already pending is idempotent; its status and response body
      are frozen by a contract test before implementation.
- [ ] Relay supports concurrent workers, stable TaskID, duplicate enqueue, retry, and
      crash between enqueue/mark-published without lost or repeated domain effect.
- [ ] Provider task loads immutable records at execution time, uses the stable
      idempotency key, retries transient failures exponentially up to ten attempts,
      and keeps exhausted sessions pending.
- [ ] Fake provider supports deterministic verified/rejected scenarios, bounded
      reasons, delay, and duplicate callback count.
- [ ] Webhook accepts lowercase/uppercase hex signatures, rejects malformed/invalid
      signatures before decoding, and never logs/stores invalid bodies.
- [ ] Valid callback atomically deduplicates event, guards state, applies verified or
      rejected verdict, appends exactly one `verification_passed` or
      `verification_failed` Session Event, and marks the event applied.
- [ ] Duplicate and late callbacks return `200` with no repeated transition/event.
- [ ] Full successful and rejected API-worker-provider E2E flows pass using disposable
      PostgreSQL, MinIO, and Redis.

## API examples

```http
POST /verification-sessions/{id}/submit HTTP/1.1
Authorization: Bearer <resume-token>

HTTP/1.1 202 Accepted
Content-Type: application/json

{"id":"<session-uuid>","status":"verification_pending"}
```

```http
POST /webhooks/verification HTTP/1.1
x-signature: sha256=<hex-hmac-of-exact-body>
Content-Type: application/json

{"eventId":"<event-uuid>","sessionId":"<session-uuid>","verdict":"verified"}
```

Rejected webhook events add exact field `reason` with one bounded rejection reason.
The behavior for a signature-valid, structurally-valid event whose `sessionId` is
unknown remains an explicit Checkpoint 5 decision gate; do not infer it from the
approved late/out-of-order behavior for known sessions.

Provider rejection reasons are exactly `document_invalid`, `biometric_mismatch`,
`identity_not_verified`, and `suspected_fraud`.

## SQL proof

The user-authored proof covers atomic pending-state/event/outbox commit and forced
rollback. Additional proofs cover safe concurrent outbox claim, publication replay,
provider event ID deduplication, and guarded/ignored late callback recording.

## Tests

- User-authored raw-body HMAC test, followed by malformed, invalid, uppercase/lowercase,
  duplicate, and late cases.
- Application submit/verdict tests with small fakes.
- PostgreSQL outbox/webhook transaction and concurrency tests.
- Real Redis relay/task idempotency and crash-window tests.
- Fake-provider verified/rejected/delay/duplicate scenarios.
- Full E2E success and rejection flows.

## Verification commands

Run the full quality gate plus explicit PostgreSQL, MinIO, Redis, and E2E commands.
No dependency-backed suite may silently skip. Record real-container versions and
failure-recovery commands.

## Implementation evidence and learning note

Retain reviewed checkpoint files, atomic SQL output, invalid-signature no-row proof,
relay crash/retry output, stable IDs, and both E2E scenarios. Add
`docs/learning/009-outbox-and-signed-webhooks.md`.

## Blocked by

- [008 - Confirm Biometric Capture uploads](./008-confirm-biometric-capture-uploads.md)
- Required user-authored checkpoints described above.
