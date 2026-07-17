# Immutable Personal Details

Issue 006 adds the first authenticated applicant command that changes three related
records atomically: immutable Personal Details, Verification Session state, and a
safe Session Event.

## Compatibility before implementation

The TypeScript request schema and route tests were inspected before Go code was
written. The frozen evidence is in
[`docs/compatibility/006-legacy-personal-details.md`](../compatibility/006-legacy-personal-details.md).
The important trap was presence versus emptiness: all four properties must exist,
but `fullName`, `identityNumber`, and `address` accept empty strings. A pointer-based
HTTP request DTO preserves that distinction without tightening the old contract.

The legacy error handler also turns malformed, empty, or multi-value JSON into
`500 INTERNAL`, while schema-invalid JSON returns `400 VALIDATION_ERROR`. The Go
adapter preserves that observable behavior even though a new API would normally map
all malformed JSON to `400`.

## One-to-one immutable data

`personal_details.verification_session_id` is both its primary key and a foreign key.
PostgreSQL therefore admits at most one row for a Verification Session. Birth date is
a PostgreSQL `date`, because it is a calendar day rather than an instant. The table
has `created_at` but no `updated_at`, and the application query surface has no update
or delete operation for Personal Details.

Text values are `NOT NULL` but have no non-empty check. That is deliberate legacy
compatibility, not missing validation.

## Authorized replay and atomic submission

The use case owns the transaction and follows this order:

1. lock the Verification Session row;
2. verify the resume token and expiry;
3. only after authorization, read existing Personal Details;
4. return the current summary for an identical replay, or conflict for a different
   replay;
5. for the first submission, insert details, guard the state transition, and append
   one `submit_personal_details` event with empty metadata;
6. commit all three writes together.

Reading Personal Details only after token and expiry checks keeps PII outside
unauthorized paths. Returning an error from any step rolls back every write.

`SELECT ... FOR UPDATE` serializes submissions for one session. A waiting identical
submission sees the winner's row and succeeds idempotently. A waiting different
submission sees the winner's row and returns `PERSONAL_DETAILS_CONFLICT`. Real
PostgreSQL tests use separate connections to prove both outcomes without timing
sleeps.

## Evidence

- `sql/proofs/004_immutable_personal_details.sql` proves the one-to-one constraint,
  successful three-write commit, safe empty event metadata, and forced rollback.
- `tests/integration/personal_details_postgres_test.go` proves the migration,
  application adapter, HTTP-to-PostgreSQL path, and identical/different concurrency.
- `internal/application/personaldetails/service_test.go` proves first submit, replay,
  conflict, auth, expiry, wrong/terminal state, and that unauthorized paths do not
  read details.
- `internal/adapter/httpapi/router_test.go` freezes fields, empty semantics, strict
  decoding, legacy framing failures, error envelopes, body limit, and redacted logs.

The complete verification command is `make quality`. Focused relational proofs run
with `go test ./tests/integration -run 'PersonalDetails|Concurrent'` while Docker is
available.

