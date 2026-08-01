# 001 — Runnable Go SQL Lab

## What this slice establishes

The first slice deliberately crosses the real boundaries once: environment to typed
configuration, OS signal to HTTP shutdown, handwritten SQL to PostgreSQL, and SQL to
generated Go types. It avoids session HTTP routes and speculative service/repository
interfaces.

## Typed configuration

`config.Load` uses `os.LookupEnv` so an unset or empty required value is distinct from
a safe default. `DATABASE_URL` is required; HTTP address, shutdown duration, and log
level have local defaults. Durations and log levels are parsed once at startup.
Validation errors name the invalid key but never interpolate its value, which keeps a
database credential out of startup logs.

## Graceful shutdown

The API owns an `http.Server` with bounded read-header, read, write, and idle
timeouts. SIGINT or SIGTERM cancels the root context. `httpserver.Run` then creates a
separate timeout-bounded shutdown context and waits for `Serve` to finish. Its test
uses a listener that signals entry into `Accept` and blocks until closed, proving the
startup/cancellation ordering without timing sleeps.

## PostgreSQL owns initial values

The migration gives PostgreSQL responsibility for the Verification Session UUID,
initial `created` state, and timestamps. The raw proof omits these columns during
insert, asserts their returned values, and catches SQLSTATE `23514` when an unknown
public state violates the bounded check constraint. This makes the database behavior
observable independently of Go and sqlc.

## Forward-only migration discipline

Migration `00001` has exactly one Goose `Up` annotation and no `Down` section. Goose
runs it transactionally by default. Routine reset/redo/down commands are intentionally
absent: a deployed schema is repaired with a later forward migration. Because the
file is Up-only, it is also safe to execute directly with `psql` for the disposable
integration proof.

## Tooling decisions

- Go language/tool dependency floor: `1.26.5`.
- sqlc: `v1.30.0`. Its version remains independently pinned; upgrading project tools
  is outside the Go toolchain upgrade.
- Goose: `v3.27.1`.
- Staticcheck: `2026.1` (`v0.7.0`).
- Testcontainers for Go: `v0.39.0`.
- PostgreSQL image: `postgres:18.4-alpine3.23`.

Goose configuration lives in the `.env.example` environment contract and Makefile.
The current Goose CLI has no `goose.yaml` configuration surface, so adding that file
would falsely imply behavior the tool does not implement.

## Verification evidence

Captured on 2026-07-14 (Asia/Jakarta).

The Go output below records the original Issue 001 environment. The project baseline
was later upgraded to Go `1.26.5` on 2026-07-28.

### Versions

```text
go version go1.25.12 darwin/amd64
sqlc v1.30.0
goose version: v3.27.1
staticcheck 2026.1 (v0.7.0)
Docker Server Version: 29.1.3
Testcontainers for Go Version: v0.39.0
PostgreSQL image: postgres:18.4-alpine3.23
```

### SQL exercise and proof

The disposable PostgreSQL test migrated a zero-state database, then executed the
committed exercise with `psql -f`. Its insert and subsequent select returned the same
database-generated row:

```text
id                                   | status  | created_at                     | updated_at
82d0147d-26d6-424a-a34e-3850ea078d04 | created | 2026-07-14 09:59:57.360161+00 | 2026-07-14 09:59:57.360161+00
INSERT 0 1
ROLLBACK
```

The assertion-style proof reported:

```text
NOTICE: proof passed: database UUID, created state, timestamps, and bounded status
ROLLBACK
```

The Go-side integration assertion separately observed PostgreSQL SQLSTATE `23514`
for `unknown_public_state`.

### Health request and graceful stop

```http
GET /health/live HTTP/1.1
Host: 127.0.0.1:18080

HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 16

{"status":"ok"}
```

The process emitted JSON lifecycle logs:

```json
{"level":"INFO","msg":"API listening","address":"127.0.0.1:18080"}
{"level":"INFO","msg":"API stopped"}
```

### Quality gate

```text
test -z "$(gofmt -l .)"                                    PASS
go vet ./...                                                PASS
staticcheck ./...                                           PASS
go test -race ./...                                         PASS
  internal/adapter/httpapi                                  PASS
  internal/platform/config                                  PASS
  internal/platform/httpserver                              PASS
  internal/platform/logging                                 PASS
  tests/integration                                         PASS
sqlc generate + generated-directory diff                    PASS (no diff)
goose -dir sql/migrations validate                          PASS
docker compose config --quiet                               PASS
```
