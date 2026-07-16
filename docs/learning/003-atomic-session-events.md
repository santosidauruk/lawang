# Atomic Session Events

The application owns the transaction boundary because it knows which business
operations must succeed together. `RecordPersonalDetailsSubmission` opens one
application-consumed transaction callback, performs the guarded state update, then
appends `submit_personal_details`. The PostgreSQL adapter supplies begin, commit, and
rollback mechanics without exposing pgx, sqlc, or pgtype to application/domain code.

The callback shape is deliberately narrow. It is not a generic repository or unit of
work, and it does not implement Issue 004's complete state machine. The single
`created -> personal_details_submitted` guard is the Issue 003 transaction proof; the
later Personal Details use case will add its own immutable record to the same atomic
boundary.

If the guarded update, append, callback, or commit fails, the adapter's deferred
rollback prevents a partially advanced session. The application fake injects a
failure during append, while the real PostgreSQL integration proof forces a failure
after the guarded update. Both leave the session in `created` with zero events. The
success path commits one state update and one event.

Session Events are append-only at two boundaries. The named application queries only
append and list; the migration trigger also rejects direct `UPDATE` and `DELETE`.
Reads order by `occurred_at, id`, so equal occurrence times have a stable UUID
tie-breaker.

Event types are action verbs, not resulting public states. Both Go parsing and the
database check admit exactly the seven canonical values. Metadata has no free-form
map: Go exposes only an optional bounded outcome, and PostgreSQL admits only `{}`,
`{"outcome":"accepted"}`, or
`{"outcome":"local_validation_failed"}`. Tests reject Personal Details, resume
tokens, object URLs, identity numbers, addresses, and raw extraction/provider bodies.

Implementation evidence:

- `go test ./internal/...`
- `go test -v ./tests/integration -run TestVerificationSessionMigrationAndSQLProof -count=1`
- `go test -race ./...`
- `make quality`
- `sql/proofs/003_atomic_session_events.sql` reports:
  `proof passed: atomic commit, rollback, bounded verbs/metadata, ordering, and append-only rows`
- The proof's ordered output is `submit_personal_details` at event ID `...0001`, then
  `confirm_identity_document` at event ID `...0002` for the same occurrence time.
- `internal/architecture/dependencies_test.go` proves application/domain packages do
  not import pgx, sqlc, or pgtype and the Session Event query surface has no update or
  delete operation.
- The existing create/resume HTTP integration test runs all three migrations and
  preserves the Issue 002 request/response contract.
