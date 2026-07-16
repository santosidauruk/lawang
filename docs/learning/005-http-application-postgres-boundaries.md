# HTTP, Application, and PostgreSQL Boundaries

Issue 005 hardens the existing create/resume path without adding a product command.
`cmd/api` remains the composition root: it constructs the pgx pool, PostgreSQL
adapter, application service, native HTTP handler, request logger, and bounded HTTP
server. An architecture test rejects runtime composition imports outside `cmd/*` and
continues to prove that application and domain code do not import pgx, sqlc, or other
adapter types.

## Native HTTP ownership

The adapter owns HTTP-only concerns. Route handlers parse Bearer credentials and
UUID path values, call the application service with `request.Context()`, and convert
application results into explicit response DTOs. Small helpers own method checks and
JSON error encoding; unsupported methods preserve `405` and `Allow` while returning
the public `METHOD_NOT_ALLOWED` envelope instead of `net/http`'s plain-text default.

Request logging wraps the completed handler in `cmd/api`. Every request receives an
`X-Request-ID`; the JSON completion event records request ID, method, matched route,
status, duration, and the bounded public error code. It never records the
Authorization header, resume token, internal error text, or response body.

There is deliberately no request decoder yet. Through Issue 005,
`POST /verification-sessions` has a bodyless compatibility contract and the resume
route is `GET`. Adding an unused generic decoder, or inventing a JSON create body,
would be speculative. Issue 006 must introduce strict size limiting,
`DisallowUnknownFields`, exactly-one-value decoding, validation, and explicit DTO
conversion together with the first JSON request contract.

## Cancellation and shutdown

One context flows through the complete read path:

```text
HTTP request context -> session use case -> SessionStore -> sqlc query -> pgx
```

Application and adapter tests prove each consumed boundary, and a disposable
PostgreSQL integration test cancels the HTTP request before the resume query. pgx
returns `context.Canceled`; the HTTP adapter emits only the safe `500 INTERNAL`
envelope. The native server already bounds read-header/read/write/idle time and its
signal context triggers graceful shutdown with a separate shutdown deadline.

## Verification evidence

The completed proof uses:

```text
go test -race ./cmd/... ./internal/...
go test -race ./tests/integration -run '<focused Issue 001-005 paths>'
make quality
```

`make quality` includes formatting, vet, staticcheck, the race-enabled full suite,
sqlc clean-diff generation, migration validation, and Compose validation. The
focused cancellation tests use Testcontainers PostgreSQL and do not touch the
developer database.
