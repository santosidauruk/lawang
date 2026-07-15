# Scaffold an Isolated Runnable Go SQL Lab

Status: ready-for-human  
Type: HITL  
Labels: ready-for-human  
Source: `docs/plan-go.md` sections 1, 2, 6, 8, 13-16, and 18

## User stories covered

- US-01: Bootstrap and inspect an isolated Go/PostgreSQL service without affecting
  the TypeScript project.

## What to build

Create the first executable vertical path in this independent repository: a pinned Go
module, typed configuration, JSON logging, graceful API process, live health route,
isolated PostgreSQL development service, forward-only Goose migration, raw `psql`
exercise, and sqlc configuration. A fresh contributor can migrate a zero-state
database, prove PostgreSQL-generated Verification Session defaults, and run
`GET /health/live`.

The human approved `github.com/santosidauruk/lawang-go` as the Go module path. Do not
change that remote identity without another explicit decision.

## Scope boundaries

- Include PostgreSQL in Compose now. Redis and MinIO may be declared only if they do
  not obscure this first runnable path; their behavior belongs to later issues.
- Create no session HTTP routes beyond `/health/live`.
- Do not copy TypeScript migrations, Node dependencies, `.env`, or generated files.
- Do not add speculative repository/service interfaces.

## Domain and data invariants

- Use the separate development database `lawang_db_go`.
- Integration tests use a disposable PostgreSQL database, never the developer DB.
- PostgreSQL generates the Verification Session UUID and default `created` state.
- Migration files are sequential, Goose-annotated, Up-only, and transaction-by-default.
- Handwritten SQL is authoritative; generated sqlc code is committed and not edited.

## Acceptance criteria

- [x] The user supplies or approves the Go module path; `go.mod` does not contain an
      invented remote identity.
- [x] The supported Go 1.25 patch and required tool versions are pinned or documented.
- [x] `.env.example` contains safe local values and no real secrets.
- [x] `compose.yaml` starts an isolated PostgreSQL service for `lawang_db_go` with an
      explicit health check and named development volume.
- [x] Typed config uses `os.LookupEnv`, distinguishes required values/defaults, and
      fails startup with a safe error when invalid.
- [x] `cmd/api` logs JSON with `slog`, starts with server timeouts, serves
      `GET /health/live`, and shuts down gracefully.
- [x] Goose and sqlc configuration exist; migration 00001 creates
      `verification_sessions` with a DB-generated UUID and bounded default state.
- [x] A raw SQL exercise inserts/selects a session and is independently runnable with
      `psql` against a disposable database.
- [x] README and Makefile expose transparent setup, migrate, SQL, run, and current
      quality commands.
- [x] Relevant formatting, vet, race-test, sqlc-diff, and migration validation checks
      pass without silently skipped dependencies.

## API example

```http
GET /health/live HTTP/1.1
Host: localhost

HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

The exact health JSON must be frozen by a test before later readiness work builds on
it.

## SQL proof

Against disposable PostgreSQL, prove that an insert omitting `id` and `status`
returns a non-null UUID, status `created`, and timestamps. Also prove the bounded
status constraint rejects an unknown public state.

## Tests

- Config tests cover required, defaulted, malformed, and secret-safe error paths.
- `httptest` covers liveness response and method rejection.
- PostgreSQL integration applies migrations from zero and executes the insert/select
  proof.
- A fixed startup/shutdown test proves cancellation without timing sleeps.

## Verification commands

```sh
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
sqlc generate
git diff --exit-code -- internal/adapter/postgres/sqlc
goose -dir sql/migrations validate
```

Document the actual Compose, migration, and `psql` commands after the command surface
exists. Do not point any test command at `lawang_db_go`.

## Implementation evidence and learning note

Retain migration/proof output, health request/response, test output, generated-code
diff check, and exact dependency versions. Add `docs/learning/001-runnable-go-sql-lab.md`
explaining configuration, graceful shutdown, PostgreSQL defaults, and forward-only
migrations.

## Resolved blocker

- Human-approved Go module path: `github.com/santosidauruk/lawang-go`.
