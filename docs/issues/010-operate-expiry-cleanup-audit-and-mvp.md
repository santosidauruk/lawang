# Operate Expiry, Cleanup, Audit, and the Complete MVP

Status: needs-triage  
Type: HITL  
Labels: needs-triage  
Source: `docs/plan-go.md` sections 5.3, 9.5, 10-16, 16 Issue 10, 17.2, and 20

## User stories covered

- US-08: Expire stalled sessions, safely clean orphaned objects, inspect redacted
  history, and run the complete documented MVP.

## What to build

Complete the operable MVP with one shared idempotent expiry operation used lazily and
by scheduled work, safe orphan cleanup, redacted audit CLI, readiness, complete
OpenAPI contract, structured request/task logs, container images/topology, CI quality
gate, and a fresh-clone full demo. Production deployment remains out of scope.

## Required learning checkpoint

The user writes the first expiry race test or cleanup-safety SQL proof and reviews the
OpenAPI contract. The agent reviews race/selection correctness and contract drift,
then completes repetitive endpoint/schema documentation and sibling cases.

## Scope boundaries

- No admin HTTP API; audit is `cmd/audit --session-id <uuid>` for authorized local or
  operational use.
- No production deployment or production-readiness/compliance claim.
- No automatic deletion of accepted Verification Artifacts.
- KMS, malware scanning, production secrets/IAM, retention policy, consent/regulatory
  workflow, metrics, OpenTelemetry, and threat model remain explicitly deferred.

## Operational and safety invariants

- Applicant-stage expiry uses `expires_at`; pending-provider expiry uses
  `verification_deadline_at`.
- Lazy and scheduled expiry call the same guarded use case and converge on one
  `expired` state plus one `session_expired` event.
- Valid callbacks after expiry are recorded as bounded ignored events.
- Cleanup selects only objects for superseded, expired, or validation-failed intents
  older than 24 hours and never objects backing confirmed intents/artifacts.
- Delete is idempotent, records `object_deleted_at`, and retries transient failure.
- Liveness performs no dependency query; readiness checks required API dependencies.

## Acceptance criteria

- [ ] User-authored expiry race test or cleanup proof receives critical review.
- [ ] Lazy applicant operations and scheduled Asynq sweep use the same idempotent
      expiry operation and produce one outcome/event under a race.
- [ ] Pending-provider sessions use their separate deadline; late signed callbacks
      are stored as ignored and do not leave `expired`.
- [ ] Scheduled and manual cleanup invoke the same use case, apply the 24-hour grace
      period, retry transient failure, and record deletion idempotently.
- [ ] Cleanup query and integration test structurally exclude confirmed intents and
      every object referenced by a Verification Artifact.
- [ ] `cmd/audit --session-id` prints state and ordered redacted events without tokens,
      PII, URLs, raw webhook/extraction bodies, or object contents.
- [ ] `/health/live` performs no dependency I/O; `/health/ready` checks PostgreSQL and
      object storage with bounded timeouts and safe errors.
- [ ] API/worker logs contain safe structured fields and correlation IDs without
      sensitive data.
- [ ] `docs/openapi.yaml` defines all Issue 001-010 routes, auth, fields, public states,
      statuses, replay/conflict behavior, and error examples matching tests.
- [ ] Docker images, environment contract, Compose topology, bootstrap/migrate/run/
      worker/provider/audit/cleanup commands, and limitations are documented.
- [ ] CI runs formatting, vet, staticcheck, race tests, sqlc clean generation, Goose
      validation, integration suites, and final E2E without silent skips.
- [ ] A fresh clone can bootstrap and demonstrate create -> details -> both uploads ->
      submit -> verified/rejected -> audit using only documented commands.

## API examples

```http
GET /health/live HTTP/1.1

HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

```http
GET /health/ready HTTP/1.1

HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

Freeze exact readiness success/failure bodies in tests and OpenAPI. Audit and cleanup
remain CLI commands, not public routes.

## SQL proof

Prove one-winner expiry under lazy/scheduled concurrency, correct applicable deadline,
one expiry event, cleanup selection after grace period, and hard exclusion of every
confirmed/artifact-backed storage key. The manual cleanup command must use the same
query/use case proven here.

## Tests

- User-authored expiry race test or cleanup-safety proof and sibling cases.
- Fixed-clock lazy/scheduled expiry application and PostgreSQL concurrency tests.
- Real MinIO cleanup success, not-found idempotency, transient retry, and safety tests.
- Audit redaction fixtures.
- Liveness/readiness success, timeout, and dependency-failure contract tests.
- OpenAPI lint/contract checks against HTTP integration fixtures.
- Fresh-environment full E2E success and rejection demos.

## Verification commands

```sh
test -z "$(gofmt -l .)"
go vet ./...
staticcheck ./...
go test -race ./...
sqlc generate
git diff --exit-code -- internal/adapter/postgres/sqlc
goose -dir sql/migrations validate
```

Also run documented integration/E2E commands with real disposable PostgreSQL, MinIO,
and Redis. Verify a fresh bootstrap rather than relying on existing volumes.

## Implementation evidence and learning note

Retain checkpoint review, expiry/cleanup SQL output, protected-object tests, audit
redaction output, OpenAPI parity results, complete gate output, and fresh-clone demo
transcript. Add `docs/learning/010-operable-mvp.md` covering expiry convergence,
cleanup safety, readiness, audit minimization, and remaining production limitations.

## Blocked by

- [009 - Submit verification and apply signed verdicts](./009-submit-verification-and-apply-signed-verdicts.md)
- Required user-authored checkpoint and OpenAPI review described above.

