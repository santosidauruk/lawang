# Lawang Go Rebuild Plan

Status: approved implementation plan  
Audience: an implementation agent starting from an empty, separate repository  
Document language: English  
Target collaboration language: Indonesian  

## 1. Mandate

Build a new Go implementation of Lawang as a completely independent project. The
new project must reproduce the observable behavior delivered by TypeScript issues
#1 through #6 before continuing with issues #7 through #10. It must use a separate
database and must not share runtime code, migration history, generated files, or
deployment lifecycle with the TypeScript project.

The TypeScript repository is evidence for the existing contract, not a dependency
of the Go service. Translate behavior and domain rules, not source-code structure.

The implementation agent is authorized to complete issues #1 through #6
autonomously. From issue #7 onward, use the learning-first collaboration checkpoints
defined in this document.

### 1.1 Fixed project boundary

- Suggested repository name: `lawang-go`.
- Determine the Go module path from the new repository's Git remote. If there is no
  remote, ask the user for the module path. Do not invent a GitHub account or module
  path.
- Use Go `1.26.5` as the baseline. Pin the exact supported patch version in the
  module declaration.
- Use a separate PostgreSQL database:

  ```dotenv
  DATABASE_URL=postgresql://lawang:lawang@localhost:5432/lawang_db_go
  ```

- Sharing a PostgreSQL server process with the old project is acceptable. Sharing a
  database, schema, migration table, or writer is not.
- Never point integration tests at `lawang_db_go`; tests must create disposable
  databases or containers.

### 1.2 Goals

1. Preserve API and domain compatibility for issues #1 through #6.
2. Teach Go, HTTP, transactions, PostgreSQL, storage, and async-processing
   fundamentals before adding abstraction.
3. Use pragmatic Clean Architecture with enforceable dependency direction.
4. Keep handwritten SQL as the source of truth.
5. Deliver direct object uploads through Upload Intents, deterministic local document
   validation, asynchronous provider submission, signed webhook handling, expiry, and
   an audit trail through issue #10.
6. Leave the repository understandable and executable by another agent without
   access to this conversation.

### 1.3 Non-goals through issue #10

- No frontend.
- No production deployment.
- No real OCR, facial recognition, or third-party verification provider.
- No ORM or query builder.
- No mandatory external HTTP framework.
- No Kafka, BullMQ, or multi-language worker.
- No admin HTTP API.
- No automatic deletion of accepted Verification Artifacts.
- No claim that the MVP is production-ready or regulatory-compliant.
- No line-by-line TypeScript port.

## 2. Decision Summary

| Area | Decision | Reason |
| --- | --- | --- |
| HTTP | Standard library `net/http` | Exposes Go's native handler, middleware, routing, JSON, and context model. Modern method/path patterns are sufficient. |
| Database driver | `github.com/jackc/pgx/v5` with `pgxpool` | Native PostgreSQL behavior, explicit transactions, and no `database/sql` indirection. |
| Query generation | `sqlc` | Generates typed Go from SQL that the learner writes and proves first. |
| Migration runner | Goose | Simple SQL-first, forward-only workflow with transaction-by-default behavior. |
| Validation | Hybrid: decoder + `go-playground/validator/v10` + manual parsers | Structural DTO validation stays concise while semantics and token extraction remain explicit. |
| UUID | `github.com/google/uuid`; PostgreSQL generation | Domain-friendly UUID type without leaking pgx types. |
| Architecture | Pragmatic Clean Architecture | Protects domain/application rules without interface-and-mapper ceremony for every type. |
| Object storage | MinIO locally through AWS SDK for Go v2 | Local S3-compatible service while learning the production-relevant AWS API. |
| Queue | Redis + Asynq | Go-native job processing with retries, delays, uniqueness, and scheduling. |
| Async reliability | PostgreSQL transactional outbox | Prevents a committed domain transition from being lost when Redis is unavailable. |
| Fake provider | Separate Go HTTP service | Preserves the real asynchronous provider boundary and callback flow. |
| Logging | Standard library `log/slog`, JSON | Structured logging without adding a second logging abstraction. |
| Configuration | Typed config using `os.LookupEnv` | Makes defaults, required values, and startup failures visible. |
| Integration tests | Testcontainers for Go | Disposable real PostgreSQL, MinIO, and Redis dependencies. |
| API specification | Explicit `docs/openapi.yaml` in issue #10 | Contract is reviewable and testable instead of inferred from handlers. |

## 3. Framework and Library Rationale

### 3.1 HTTP choices

#### Chosen: `net/http`

Use `http.ServeMux` method/path patterns, `Request.PathValue`, explicit JSON helpers,
and ordinary middleware. This is enough for the planned API and exposes the
fundamentals that frameworks wrap: request lifetime, response ownership, middleware
composition, cancellation, and error translation.

Do not create an internal pseudo-framework. A few focused helpers for JSON responses,
errors, authentication, and request IDs are acceptable; a generic handler DSL is not.

#### Optional later: Chi

Chi is the first framework/router to reconsider only after issue #10 and only when a
concrete routing or middleware limitation appears. It composes with `net/http`, so the
cost of adoption is low. It is not a planned migration and must not be added merely
for convention.

#### Not chosen: Gin

Gin is mature and productive, but its custom context and binding conventions would
move the learning target away from native `net/http`. It is useful for teams already
optimizing feature throughput; that is not the primary constraint here.

#### Not chosen: Echo

Echo has similar convenience and similar abstraction cost: custom context, binding,
and framework middleware APIs. It offers no necessary capability for this MVP.

#### Not chosen: Fiber

Fiber is built around `fasthttp`, not `net/http`. That ecosystem difference affects
middleware compatibility and handler assumptions. Its performance-oriented tradeoff
is unnecessary here and weakens the fundamentals-first goal.

### 3.2 Database choices

#### Chosen: pgx/v5 + sqlc

Write and prove SQL manually, then let sqlc generate typed call sites. Keep the sqlc
package private to the PostgreSQL adapter. Application and domain packages must not
import sqlc, pgx, or `pgtype`.

#### Alternatives

- Handwritten pgx calls maximize direct learning but create repetitive row scanning
  and increase the chance that query result shapes drift from Go code.
- `database/sql` with pgx's compatibility driver is more portable, but portability is
  not a requirement and it hides useful PostgreSQL-specific behavior.
- Bun is SQL-oriented, but still adds a query-building and model-mapping layer before
  the raw SQL workflow is established.
- GORM hides query shape and persistence behavior behind ORM conventions.
- Ent provides strong generated models but makes its schema DSL, rather than SQL, the
  primary source of truth.

Do not add GORM, Ent, Bun, Squirrel, or another query builder through issue #10.

### 3.3 Goose versus golang-migrate

Goose is selected, but its operational limitation must be explicit.

| Concern | Goose | golang-migrate |
| --- | --- | --- |
| SQL layout | Can use one annotated file per migration | Conventionally paired `.up.sql` and `.down.sql` files |
| Transactions | SQL migrations run in a transaction by default; opt out only when required | Transaction control is explicit in migration SQL/driver behavior |
| Dirty state | Records applied versions; failures surface normally | Has an explicit dirty state and a `force` recovery mechanism |
| Locking | Do not assume cross-process locking; serialize migration execution externally | PostgreSQL driver commonly uses advisory locking |
| Fit here | Simple forward-only SQL files and a small learning project | Strong when paired migration tooling and explicit recovery controls are preferred |

Rules for this project:

- Use sequential files such as `00001_create_verification_sessions.sql`.
- Use Goose SQL annotations and an `Up` section only.
- Never provide routine `down`, `reset`, or `redo` workflows. Repair production-like
  state with a new forward migration.
- Keep the default transaction. Use `NO TRANSACTION` only for a PostgreSQL operation
  that cannot run in a transaction, and document why in the migration.
- Never run migrations automatically when the API starts.
- Apply migrations through an explicit CLI command or one serialized CI/deployment
  job. Goose must not be invoked concurrently.
- Keep SQL exercises and proofs independently executable with `psql`.
- An Up-only file is important because Goose annotations are SQL comments: a file
  containing a Down section can accidentally run both sections when passed directly
  to raw `psql`.

### 3.4 AWS SDK v2 versus MinIO-specific SDK

Use AWS SDK for Go v2 with its S3 client and presign client. It provides the broader
production S3 API and avoids replacing the storage client if AWS S3 is adopted later.
The MinIO-specific SDK is simpler for a MinIO-only system and provides convenient
helpers, but it is not selected because the desired learning boundary is S3 rather
than one server implementation.

Hide all SDK types behind the `ObjectStorage` application port. This limits the cost
if the SDK decision changes.

### 3.5 Asynq versus BullMQ and Kafka

- Asynq matches job semantics: retries, exponential backoff, delayed execution,
  uniqueness, task IDs, and scheduled work. It keeps the worker in Go.
- BullMQ also matches job semantics well, but requires a Node.js runtime and creates
  a polyglot operational boundary solely for the queue.
- Kafka is a durable event log, not a job queue with native retry/backoff semantics.
  It is operationally disproportionate for this service.

Asynq has pre-1.0 API-stability risk. Pin an exact version and isolate it in
`internal/adapter/queue`. Do not expose `asynq.Task` to application code.

## 4. Domain Model and Vocabulary

Use one bounded context: **Verification**. Use these terms consistently in code,
schema, events, API documentation, and tests.

| Term | Meaning |
| --- | --- |
| Applicant | Person undergoing verification. |
| Verification Session | Time-limited aggregate that owns one verification attempt. |
| Personal Details | Immutable applicant identity data submitted once per session. |
| Identity Document | Document evidence such as an identity card. |
| Biometric Capture | Selfie or other biometric evidence. |
| Upload Intent | Temporary permission and storage key for one direct upload attempt. It is not evidence. |
| Verification Artifact | Accepted evidence recorded after object checks and any local validation succeed. |
| Local Validation Failure | Deterministic rejection before a provider submission. |
| Verification Provider | External system that evaluates accepted evidence. |
| Provider Submission | Asynchronous request sent to the provider. |
| Verification Verdict | Provider outcome: verified or rejected with a bounded reason. |
| Webhook Event | Signed provider callback, deduplicated by provider event ID. |
| Session Event | Append-only normalized audit event whose bounded type names the action applied to a session. |
| Expired Session | Terminal session that exceeded its applicable deadline. |

Never use `Artifact` alone when the intended meaning is accepted evidence. Distinguish
the three storage concepts explicitly:

1. Upload Intent: temporary authorization and expected object metadata.
2. Stored object: bytes in S3-compatible storage.
3. Verification Artifact: accepted database record that the domain may consume.

### 4.1 Session states

The exact public strings are:

```text
created
personal_details_submitted
identity_document_uploaded
biometric_capture_uploaded
verification_pending
verified
rejected
expired
```

Required forward path:

```text
created
  -> personal_details_submitted
  -> identity_document_uploaded
  -> biometric_capture_uploaded
  -> verification_pending
  -> verified | rejected
```

`expired` can be entered from any non-terminal state when its applicable deadline is
exceeded. `verified`, `rejected`, and `expired` are terminal; all later transition
attempts must fail without partial writes.

Biometric-before-document is intentionally not supported in this plan because the
public state contract encodes document then biometric. Changing that order requires
an explicit domain and API revision.

### 4.2 Session Event action verbs

Session Event types are action-based. The database constraint, generated query
surface, Go domain type/constants, tests, audit output, and documentation use exactly:

```text
submit_personal_details
confirm_identity_document
confirm_biometric_capture
submit_session
verification_passed
verification_failed
expire
```

Do not reuse result/state strings as event types. Session state describes the result
of a transition; the Session Event type describes the action applied. The canonical
successful mapping is:

| Session Event type | Resulting public state |
| --- | --- |
| `submit_personal_details` | `personal_details_submitted` |
| `confirm_identity_document` | `identity_document_uploaded` |
| `confirm_biometric_capture` | `biometric_capture_uploaded` |
| `submit_session` | `verification_pending` |
| `verification_passed` | `verified` |
| `verification_failed` | `rejected` |
| `expire` | `expired` |

The same `confirm_identity_document` type records a completed confirmation that ends
in a bounded Local Validation Failure without a state transition. Its safe metadata
distinguishes the bounded outcome, for example `accepted` from
`local_validation_failed`; do not add a result-named event type. Idempotent replay
does not append another event. Valid ignored provider callbacks remain Webhook Events
and do not create a Session Event.

### 4.3 Aggregate invariants

- Resume tokens are random opaque credentials. Store only their cryptographic hash.
- A session has one immutable Personal Details record.
- An identical Personal Details replay succeeds without a second event.
- A different Personal Details replay returns a conflict.
- A session has at most one pending Upload Intent for each evidence kind.
- A new intent supersedes an older pending intent and receives a new unique key.
- An Upload Intent produces at most one Verification Artifact.
- A Verification Artifact is created only after storage metadata checks and local
  validation succeed.
- A session requires accepted Identity Document and Biometric Capture Verification
  Verification Artifacts before Provider Submission.
- Every state transition and its Session Event commit atomically.
- The database and Go domain layer admit only the seven Session Event action verbs in
  section 4.2; arbitrary strings are invalid.
- External network or object-storage I/O never occurs while a database transaction is
  open.
- Terminal states never transition.

## 5. Public API Contract

Compatibility means preserving route, method, status, JSON field names, public state
strings, error envelope, token behavior, replay behavior, and conflict behavior. It
does not require matching TypeScript internal types or package structure.

### 5.1 Error envelope

All expected errors use:

```json
{
  "code": "MACHINE_READABLE_CODE",
  "message": "Human-readable summary",
  "details": {}
}
```

`details` is optional. Never include secrets, identity numbers, addresses, raw OCR,
object URLs, stack traces, or internal SDK errors.

### 5.2 Issue #1–#6 compatibility routes

#### `POST /verification-sessions`

- Response: `201 Created`.
- Generates the persisted UUID in PostgreSQL.
- Generates a high-entropy raw resume token, stores only its hash, and returns the raw
  token once.

```json
{
  "id": "uuid",
  "status": "created",
  "expiresAt": "RFC3339 timestamp",
  "resumeToken": "opaque token"
}
```

#### `GET /verification-sessions/{id}`

- Requires `Authorization: Bearer <token>`; scheme matching is case-insensitive and
  the credential is one non-whitespace value.
- Response: `200 OK`.

```json
{
  "id": "uuid",
  "status": "created",
  "expiresAt": "RFC3339 timestamp"
}
```

#### `POST /verification-sessions/{id}/personal-details`

- Requires the resume token.
- Response: `200 OK` with the current session summary.
- First valid request inserts immutable details, advances state, and appends exactly
  one event in one transaction.
- Identical replay returns `200` and creates no write or event.
- Different replay returns `409 Conflict`.
- Preserve the old request field names and exact validation semantics by extracting
  contract examples from issue #6 tests before implementation. In particular, a
  TypeScript `z.string()` field may have required presence while still allowing an
  empty string. Do not silently add `required,min=1` unless the existing contract
  already rejects empty values.

### 5.3 Issue #7–#10 routes

#### `POST /verification-sessions/{id}/artifacts/upload-url`

Request:

```json
{ "kind": "identity_document" }
```

Return a new Upload Intent ID and presigned upload URL. Supported kinds are
`identity_document` and `biometric_capture`. Do not return internal credentials.

#### `POST /verification-sessions/{id}/artifacts/confirm`

Request:

```json
{ "uploadIntentId": "uuid" }
```

- Success returns `200` with the current session summary.
- Confirm replay for an already confirmed intent returns the same success and creates
  no duplicate Verification Artifact or event.
- Superseded or expired intent returns `409`.
- Local document mismatch returns `422`:

```json
{
  "code": "LOCAL_VALIDATION_FAILED",
  "message": "The uploaded identity document did not match the submitted details",
  "details": { "reason": "identity_number_mismatch" }
}
```

Repeating confirm on an intent with `validation_failed` returns the same bounded `422`
without calling storage or the extractor again.

#### `POST /verification-sessions/{id}/submit`

- Requires both accepted Verification Artifacts.
- Atomically advances to `verification_pending`, writes the Session Event and outbox
  row, then returns `202 Accepted`.
- It does not wait for Redis or the fake provider.
- An exact replay while already pending is idempotent; define and contract-test the
  response body before implementation.

#### `POST /webhooks/verification`

- Provider-facing signed endpoint; it does not use a resume token.
- Signature header: `x-signature: sha256=<lowercase-or-uppercase-hex>`.
- Invalid signature returns `401` and stores nothing.
- Valid duplicate or valid out-of-order callbacks return `200` without repeating a
  state transition.

#### Health endpoints

- `GET /health/live`: process is running; no dependency query.
- `GET /health/ready`: required dependencies are reachable. API readiness checks
  PostgreSQL and object storage. Worker readiness is exposed through process/container
  health appropriate to the deployment topology.

## 6. Pragmatic Clean Architecture

### 6.1 Dependency rule

```text
cmd composition -> adapters -> application -> domain
```

The domain imports only the Go standard library plus a deliberately chosen value-type
library such as `google/uuid`. The application imports the domain and defines ports it
consumes. Adapters implement those ports. Only `cmd/*` knows concrete implementations
and wires them together.

### 6.2 Target layout

```text
lawang-go/
├── cmd/
│   ├── api/main.go
│   ├── worker/main.go
│   ├── fake-provider/main.go
│   ├── audit/main.go
│   └── bootstrap/main.go
├── internal/
│   ├── verification/
│   │   ├── domain/
│   │   │   ├── session.go
│   │   │   ├── state_machine.go
│   │   │   ├── verification_artifact.go
│   │   │   ├── events.go
│   │   │   └── errors.go
│   │   └── application/
│   │       ├── ports.go
│   │       ├── create_session.go
│   │       ├── get_session.go
│   │       ├── submit_personal_details.go
│   │       ├── create_upload_intent.go
│   │       ├── confirm_upload.go
│   │       ├── submit_verification.go
│   │       ├── apply_verdict.go
│   │       └── expire_sessions.go
│   ├── adapter/
│   │   ├── httpapi/
│   │   │   ├── router.go
│   │   │   ├── middleware.go
│   │   │   ├── validation.go
│   │   │   ├── errors.go
│   │   │   └── handlers/
│   │   ├── postgres/
│   │   │   ├── store.go
│   │   │   ├── transaction.go
│   │   │   └── sqlc/
│   │   ├── objectstorage/
│   │   ├── extractor/
│   │   ├── provider/
│   │   └── queue/
│   └── platform/
│       ├── config/
│       └── logging/
├── sql/
│   ├── migrations/
│   ├── queries/
│   ├── exercises/
│   └── proofs/
├── tests/
│   ├── integration/
│   ├── e2e/
│   └── testutil/
├── docs/
│   ├── issues/
│   ├── learning/
│   ├── adr/
│   └── openapi.yaml
├── compose.yaml
├── goose.yaml
├── sqlc.yaml
├── go.mod
├── go.sum
├── Makefile
├── CONTEXT.md
└── README.md
```

Adjust filenames when cohesion improves, but preserve boundaries. Do not create a
top-level package per technical noun if it fragments one small use case.

### 6.3 Pragmatic interface rules

Avoid the ceremonial variant where every entity has an interface, DTO, mapper,
repository, service, service implementation, and factory. That structure multiplies
files without creating an actual substitutable boundary.

Use these rules instead:

- Define an interface at the consumer, normally in the application package, only for
  database transactions, clocks/randomness when tests need control, object storage,
  document extraction, provider submission, and queue/outbox coordination.
- Use concrete domain structs and concrete use-case structs.
- Keep simple row-to-domain mapping as a private adapter function.
- Create a dedicated mapper only when a transformation is complex, reused, or carries
  semantic rules.
- Do not create generic repositories or base services.
- Do not use a dependency-injection framework. Wire constructors in `cmd/*`.
- Never expose pgx rows, sqlc models, HTTP DTOs, AWS SDK values, or Asynq tasks to the
  application/domain layers.

Illustrative shape:

```go
// Defined by the application because it consumes this behavior.
type SessionStore interface {
	WithTx(ctx context.Context, fn func(SessionTx) error) error
	FindAuthorized(ctx context.Context, id uuid.UUID, tokenHash []byte) (domain.Session, error)
}

type SubmitPersonalDetails struct {
	store SessionStore
	clock Clock
}
```

Do not introduce `ISession`, `SessionMapperInterface`,
`SessionRepositoryImplementation`, and `BaseUseCase` merely to mirror an enterprise
template.

### 6.4 Context and transaction rules

- `context.Context` is the first parameter of I/O-bound methods.
- Never store context in a struct or domain entity.
- Application use cases own transaction boundaries; the PostgreSQL adapter supplies
  the transaction mechanism.
- A use case must not perform S3 or HTTP calls inside a DB transaction.
- For flows with external I/O, read first, perform I/O, then start a short transaction
  and re-read all state used for authorization or transition. Discard stale I/O
  results if the guarded re-read detects a conflict.

## 7. Validation, Authentication, and Time

### 7.1 HTTP decoding

For JSON endpoints:

1. Apply `http.MaxBytesReader` with a route-appropriate limit.
2. Use `json.Decoder` and `DisallowUnknownFields`.
3. Decode one object.
4. Verify that no second JSON value follows.
5. Run adapter-level validator rules.
6. Convert DTOs into domain/application input values explicitly.

Use `go-playground/validator/v10` only in the HTTP adapter for structural rules such
as:

```go
type PersonalDetailsRequest struct {
	DateOfBirth string `json:"dateOfBirth" validate:"required,datetime=2006-01-02"`
	CountryCode string `json:"countryCode" validate:"required,iso3166_1_alpha2"`
}

type ConfirmUploadRequest struct {
	UploadIntentID string `json:"uploadIntentId" validate:"required,uuid"`
}

type UploadURLRequest struct {
	Kind string `json:"kind" validate:"required,oneof=identity_document biometric_capture"`
}
```

Treat these tags as examples, not permission to tighten the old API. For compatibility
fields where presence and non-empty semantics differ, use pointer DTO fields or
explicit presence checks.

After validating a date, parse it with `time.Parse("2006-01-02", value)` and pass a
domain date value, not the original string. After validating a UUID, parse with
`uuid.Parse`.

### 7.2 Bearer tokens

Parse Bearer authorization manually because validation must also extract the secret.
Accept case-insensitive `Bearer`, exactly one credential, and no embedded whitespace.
Reject missing, empty, or multi-part credentials. Hash the extracted token with the
same deterministic algorithm used at creation and compare authorized records without
logging the token.

### 7.3 Clocks

Inject a small `Clock` interface into use cases that make expiry decisions. Persist
timestamps in PostgreSQL as `timestamptz` and exchange RFC3339 UTC timestamps at the
API. Tests must use a fixed clock; avoid timing sleeps.

## 8. Database and SQL Workflow

### 8.1 SQL-first cycle

For every schema or query change:

1. State the invariant in the issue document.
2. Write the forward Goose migration by hand.
3. Write a focused SQL exercise or proof when the behavior is primarily relational,
   transactional, or constraint-driven.
4. Run it with `psql` against a disposable database.
5. Write named queries in `sql/queries`.
6. Run sqlc generation.
7. Inspect the generated signature and types.
8. Wrap the generated query in the PostgreSQL adapter.
9. Add integration and application tests.

Raw SQL remains the source of truth. Generated sqlc files are committed, never
hand-edited, and regenerated in CI; CI fails if generation leaves a diff.

Configure sqlc so PostgreSQL UUIDs map to `github.com/google/uuid.UUID`. Prefer
nullable standard/domain-friendly types; do not allow `pgtype` to leak inward.

### 8.2 Planned migration sequence

Exact columns should be reconciled against the issue #1–#6 behavioral tests before
writing migrations, but use this forward sequence:

1. `00001_create_verification_sessions.sql`
2. `00002_add_resume_token_hash.sql` if not included in 00001
3. `00003_create_session_events.sql`
4. `00004_create_personal_details.sql`
5. `00005_create_upload_intents.sql`
6. `00006_create_verification_artifacts.sql`
7. `00007_add_provider_submission_outbox.sql`
8. `00008_create_webhook_events.sql`
9. `00009_add_expiry_and_cleanup_fields.sql` if not introduced with owning tables

When starting the new repo, it is acceptable to consolidate columns within the first
four migrations if their issue-level learning sequence and proofs remain clear. Never
copy the old Drizzle migration history or point Goose at the old database.

### 8.3 Core relational constraints

#### `verification_sessions`

- UUID primary key generated by `gen_random_uuid()`.
- bounded status check constraint using exact public state strings.
- `resume_token_hash` unique and non-null after issue #2.
- `expires_at` for applicant-stage expiry.
- `verification_deadline_at` nullable until provider submission.
- `verified_at`, `rejected_at`, `expired_at`, `rejection_reason` as applicable.
- standard created/updated timestamps.

#### `personal_details`

- one-to-one primary/unique foreign key to session.
- immutable after insert; enforce through application behavior and a focused database
  proof. A defensive trigger is optional only if its learning cost is documented.
- store full values here only, not duplicated in Session Event payloads.

#### `session_events`

- append-only event ID, session foreign key, bounded event type, safe JSON metadata,
  occurred timestamp.
- event type is constrained to the seven action verbs in section 4.2; use the same
  exact strings in SQL and typed Go constants rather than public state/result names.
- safe bounded metadata may describe an action outcome when no state transition occurs,
  such as Identity Document local validation failure.
- never include raw identity number, address, resume token, object URL, or OCR body.

#### `upload_intents`

- UUID ID, session ID, kind, unique storage key, expiry, status,
  failure code, confirmation timestamp, and `object_deleted_at`.
- statuses: `pending`, `confirmed`, `superseded`, `expired`, `validation_failed`.
- partial unique index enforcing one `pending` row per `(session_id, kind)`.
- bounded failure reasons. Do not store free-form extractor errors.

The Upload Intent stores only the bounded `kind`, not a snapshot of allowed media
types or maximum size. Application code owns the exact constraint mapping for each
kind and confirmation applies the currently deployed mapping. Tests must freeze the
Issue 007 Identity Document mapping as JPEG/PNG/PDF, non-zero, and at most 10 MiB.

The application generates the Upload Intent UUID before persistence and uses it to
construct a fresh unique storage key. Identity Document keys use
`verification-sessions/{sessionID}/identity_document/{intentID}`. After an initial
authorization/state read, it presigns that key outside a database transaction. A
short transaction then re-reads the relevant state, atomically supersedes the
previous pending intent, and inserts the new intent with the application-supplied
UUID and key. If signing fails, no intent is written. If the transactional re-read
is stale, discard the unreturned URL. Never keep the supersede/insert transaction
open while presigning.

#### `verification_artifacts`

- accepted evidence only.
- session ID, Upload Intent ID, kind, storage key, content type, size, ETag, minimized
  extraction result, and creation timestamp.
- unique Upload Intent and one accepted Verification Artifact per `(session_id, kind)`.
- no raw file bytes.

#### `outbox`

- durable ID, aggregate/session ID, task type, minimal JSON metadata, created time,
  published time, attempt/error metadata needed by the relay.
- payload contains identifiers and idempotency data, not Personal Details or object
  contents. The worker loads immutable records when executing.

#### `webhook_events`

- provider event ID primary key for deduplication.
- provider-reported session ID as a non-null UUID without a mandatory foreign key,
  valid signed raw payload, processing status (`applied` or `ignored`), bounded ignore
  reason, received and processed timestamps.
- invalid-signature bodies are never persisted.

## 9. Object Storage and Local Validation

### 9.1 Local S3 topology

Use MinIO locally and AWS SDK for Go v2 in the adapter. Configuration must distinguish:

```dotenv
S3_INTERNAL_ENDPOINT=http://minio:9000
S3_PUBLIC_ENDPOINT=http://localhost:9000
S3_REGION=us-east-1
S3_BUCKET=lawang-verification
S3_ACCESS_KEY_ID=lawang
S3_SECRET_ACCESS_KEY=local-development-only
S3_USE_PATH_STYLE=true
```

The URL returned to a browser/client must be signed using the public host. Do not sign
with `minio:9000` and rewrite it to `localhost:9000`; the HTTP host is part of the S3
signature and rewriting invalidates it. Construct appropriately configured internal
and presign clients if necessary while keeping one adapter boundary.

Bucket lifecycle:

- `cmd/bootstrap` creates the development bucket idempotently.
- Integration fixtures create their own test bucket.
- Production infrastructure creates buckets.
- The API verifies access/readiness and fails startup clearly when required storage is
  unavailable; it does not silently provision production infrastructure.

### 9.2 File constraints

| Kind | Allowed content types | Maximum |
| --- | --- | --- |
| Identity Document | JPEG, PNG, PDF | 10 MiB |
| Biometric Capture | JPEG, PNG | 5 MiB |

Reject zero-byte objects. At confirm time, use `HeadObject` and record the confirmed
content type, byte size, and ETag; do not trust request metadata alone.

Presigned URL and Upload Intent TTL are both five minutes.

### 9.3 Upload Intent behavior

Creating a new intent atomically marks an existing pending intent of the same kind as
`superseded` and creates a fresh unique storage key. A superseded presigned URL may
still upload until its signature expires; confirm must reject it, and cleanup later
removes the orphan.

Confirmation ordering:

1. Load and authorize the session and intent.
2. Return recorded idempotent outcomes for `confirmed` or `validation_failed`.
3. Reject expired/superseded intent.
4. Call `HeadObject` outside a DB transaction.
5. Enforce size and media-type rules.
6. For Identity Document only, call the deterministic `DocumentExtractor` and
   reconcile its minimized result against immutable Personal Details.
7. Start a short DB transaction.
8. Re-read the session and intent and repeat guards.
9. On local mismatch: mark `validation_failed`, persist bounded failure reason, append
   a safe `confirm_identity_document` Session Event with bounded failure metadata,
   and commit. Do not create a Verification Artifact or advance the session.
10. On success: mark confirmed, create one Verification Artifact, advance session,
    append `confirm_identity_document`, and commit.

If the transactional re-read detects that state changed while storage/extraction ran,
discard the external result and return the applicable conflict/idempotent outcome.

### 9.4 Deterministic fake extractor

Issue #7 implements a fake `DocumentExtractor`, not OCR. Its result must be controlled
by deterministic test input or object metadata/fixture convention so success and each
bounded mismatch are repeatable. At minimum prove `identity_number_mismatch`.

Do not store raw OCR output. Persist only fields explicitly required for audit or
provider submission, and prefer match outcomes over copied identity fields.

Biometric Capture in issue #8 uses storage checks but no document extractor.

### 9.5 Orphan cleanup

The worker periodically finds objects belonging to `superseded`, `expired`, or
`validation_failed` intents older than a 24-hour grace period. It must never delete an
object for a confirmed intent or accepted Verification Artifact. Delete idempotently,
set `object_deleted_at`, and retry transient failures. Provide a manual development
command that invokes the same application use case.

## 10. Asynchronous Provider Flow

### 10.1 Processes

Run three independently startable Go services:

- `cmd/api`: applicant API and provider webhook.
- `cmd/worker`: outbox relay and Asynq handlers.
- `cmd/fake-provider`: deterministic external provider simulator.

One worker binary hosts:

- outbox relay;
- `provider:submit` tasks;
- `storage:cleanup` tasks;
- `session:expire` tasks.

Do not split worker deployments before operational evidence requires independent
scaling or failure isolation.

### 10.2 Transactional outbox

Submission follows this sequence:

1. Authorize session and prove both required Verification Artifacts exist.
2. In one PostgreSQL transaction, guard current state, transition to
   `verification_pending`, set `verification_deadline_at`, append `submit_session`,
   and insert an outbox row.
3. Commit and return `202`.
4. The relay reads unpublished outbox rows and enqueues an Asynq task using the outbox
   UUID as stable `TaskID`.
5. Treat Asynq duplicate-task response as successful publication, then mark the outbox
   row published.
6. The task loads current immutable Personal Details and Verification Artifacts and
   calls the provider using the session UUID as the stable `Idempotency-Key`.

PostgreSQL and Redis cannot commit atomically. The outbox remains necessary even with
Asynq uniqueness. Relay selection must support safe concurrent workers, for example
with a short claim/lease or `FOR UPDATE SKIP LOCKED` transaction.

Provider submission retries transient errors with exponential backoff, at most ten
attempts. Exhausted integration retries do not mean applicant rejection; the session
remains pending until a callback or expiry.

### 10.3 Fake provider service

The provider accepts HTTP submissions, acknowledges them, and sends an asynchronous
signed callback to the Lawang webhook. It must support deterministic scenarios through
a test-only endpoint:

```http
PUT /test/scenarios/{sessionId}
Content-Type: application/json

{
  "verdict": "rejected",
  "reason": "document_invalid",
  "delayMs": 50,
  "duplicateCallbacks": 1
}
```

Use a default successful scenario for manual demos. The test endpoint belongs to the
fake provider and is never part of the public Lawang API.

Bounded rejection reasons:

```text
document_invalid
biometric_mismatch
identity_not_verified
suspected_fraud
```

`provider_unavailable` is an integration failure, not a Verification Verdict.

### 10.4 Webhook security and idempotency

1. Limit the request body.
2. Read raw bytes without decoding or logging them.
3. Parse `x-signature` as `sha256=<hex>`.
4. Compute HMAC-SHA256 over the exact raw bytes using the provider secret.
5. Compare signatures with `hmac.Equal`.
6. On failure, return `401`; do not parse, trust, log, or store the payload.
7. On success, decode and validate the event.
8. In one transaction, insert/deduplicate the provider event, guard the session state,
   apply a valid verdict, append `verification_passed` or `verification_failed`, and
   mark the callback applied.

A duplicate provider event ID returns `200` with no mutation. A valid but late or
out-of-order event is stored as `ignored` with a bounded reason and returns `200`.
Likewise, a signature-valid and structurally-valid callback whose reported session ID
does not resolve is stored once as `ignored:unknown_session`, returns exact
`200 {"status":"ok"}`, and creates no Verification Session mutation or Session Event.
Store its UUID in non-null `reported_session_id` without a foreign key; the ignored
outcome is final and is not automatically applied if a matching session appears
later.
HTTPS remains mandatory outside local development; HMAC supplies integrity and
authenticity, not confidentiality.

## 11. Expiry and Audit

Use both expiry paths with one shared, guarded application operation:

- Lazy expiry when an applicant operation loads an overdue non-terminal session.
- Scheduled Asynq sweep for overdue sessions with no applicant traffic.

Applicant-stage expiry uses `expires_at`. Pending-provider expiry uses
`verification_deadline_at`. The transition is idempotent and appends exactly one
`expire` event. Valid callbacks received after expiry are stored as ignored.

Provide `cmd/audit --session-id <uuid>` for authorized local/operational inspection.
It prints session state and redacted event history. Do not expose an audit HTTP route
through issue #10 because there is no admin authentication model.

## 12. Security and Data Minimization

Mandatory through issue #10:

- Store resume-token hashes only.
- Never log resume tokens, Authorization headers, presigned URLs, object contents,
  raw OCR, identity numbers, addresses, or webhook bodies.
- Personal Details has one primary database representation; Session Event metadata
  contains identifiers and safe outcomes only.
- Use environment variables for secrets and validate them at startup.
- Use constant-time HMAC comparison.
- Set JSON/body limits and server read/write/idle timeouts.
- Validate storage metadata after upload.
- Run external I/O outside transactions and re-check state atomically.
- Document that local plaintext PII is an MVP limitation.

Deferred and explicitly not solved: KMS/envelope encryption, production secret
manager, malware scanning, Verification Artifact retention/deletion policy, consent and
regulatory workflows, real IAM roles, and a production threat model. Reverse-proxy
HTTPS is required for any non-local deployment. The README must not describe the MVP
as production-ready.

## 13. Configuration and Observability

Create a typed `Config` using `os.LookupEnv`. Separate required values, safe local
defaults, durations, and secrets. Validate once before constructing services. At
minimum cover:

- HTTP address and shutdown timeout;
- `DATABASE_URL`;
- session and pending-verification TTLs;
- S3 internal/public endpoints, region, bucket, credentials, path-style mode;
- Redis address/password/database;
- provider base URL, callback URL, webhook secret;
- log level and environment.

Use `slog` JSON. HTTP middleware records request ID, method, route, response status,
duration, and safe error code. Worker logs task ID/type, attempt, duration, and outcome.
Propagate request/correlation IDs where practical. Metrics and OpenTelemetry are
deferred until after issue #10.

## 14. Testing Strategy

### 14.1 Layers

- Domain: table-driven standard-library tests, no database, filesystem, network, or
  real clock.
- Application: use-case tests with small handwritten fakes for consumed ports.
- HTTP adapter: `httptest` contract tests for decoding, authentication, status, JSON,
  unknown fields, and error mapping.
- PostgreSQL adapter: Testcontainers PostgreSQL with real migrations and sqlc queries.
- Object storage: real MinIO container, including presigned upload and `HeadObject`.
- Queue: real Redis container for relay/task idempotency where behavior depends on
  Redis.
- End-to-end: API + worker + fake provider with PostgreSQL, MinIO, and Redis.

Integration tests must never truncate or mutate the developer database. If Docker is
required and unavailable in CI, fail clearly; do not silently skip the integration
suite.

### 14.2 Required behavioral proofs

At minimum prove:

- database-generated UUID and default state;
- token hash authorization and raw-token one-time return;
- allowed/forbidden/terminal state transitions;
- transaction rollback leaves neither state nor event partially written;
- immutable Personal Details with identical/different replay behavior;
- one pending Upload Intent per kind under concurrency;
- supersede, expiry, confirm replay, and validation-failure replay;
- local mismatch creates no Verification Artifact;
- no duplicate Verification Artifact/event during concurrent confirms;
- submit transaction atomically writes state, event, and outbox;
- outbox relay tolerates crash/retry and duplicate enqueue;
- provider task uses stable idempotency key;
- invalid webhook signature stores nothing;
- duplicate and late webhooks do not repeat transitions;
- lazy and scheduled expiry converge on one outcome/event;
- orphan cleanup cannot select objects belonging to confirmed intents or Verification
  Artifacts;
- API issue #1–#6 response snapshots/contracts match the old behavior.

### 14.3 Quality gate

CI and the final local handoff run:

```sh
test -z "$(gofmt -l .)"
go vet ./...
staticcheck ./...
go test -race ./...
sqlc generate
git diff --exit-code -- internal/adapter/postgres/sqlc
goose -dir sql/migrations validate
```

Add explicit integration/E2E commands if they are separated with build tags or
packages. No required check may pass by silently skipping its dependencies.

## 15. Local Development Topology

`compose.yaml` provides PostgreSQL, Redis, and MinIO. The API, worker, and fake
provider may also have Compose profiles/images, but contributors must be able to run
their source with `go run ./cmd/...` against the containers.

Provide a Makefile or similarly transparent command surface; every target should show
the underlying standard command. Recommended targets:

```text
deps-up          start PostgreSQL, Redis, MinIO
bootstrap        create dev database extensions/bucket as applicable
migrate          apply Goose migrations to explicit DATABASE_URL
sqlc             generate typed queries
test             unit and adapter tests
test-integration disposable dependency-backed tests
test-e2e         full async flow
check            formatting, vet, staticcheck, race, generation diff, migration validation
run-api
run-worker
run-provider
audit
cleanup-orphans
```

Do not hide destructive commands behind short ambiguous names. Do not add a migration
reset target for shared environments.

## 16. Execution Plan by Issue

Each issue gets `docs/issues/NN-*.md` containing objective, domain language,
preconditions, acceptance criteria, SQL proof, API examples, tests, verification
commands, and learning notes. Each completed issue gets a matching concise entry in
`docs/learning/` explaining the fundamental learned and the evidence produced.

### Issue #1 — Scaffold a runnable Go SQL lab

Deliver:

- independent Go module and repository scaffolding;
- typed config and structured logging;
- Compose PostgreSQL using `lawang_db_go` for development;
- Goose and sqlc configuration;
- first up-only migration for Verification Session;
- raw SQL exercise inserting/selecting a session;
- minimal API process with live health endpoint;
- README commands and `.env.example` without real secrets.

Acceptance:

- fresh database migrates from zero;
- PostgreSQL generates session UUID/default status;
- exercise is independently runnable with `psql`;
- process starts and shuts down gracefully;
- quality gate relevant to current code passes.

### Issue #2 — Create and resume a Verification Session

Deliver:

- cryptographically random resume-token generation;
- token hash persistence and lookup;
- `POST /verification-sessions` and authenticated GET route;
- manual Bearer parser and constant-safe credential handling;
- exact compatibility response/error contract.

Acceptance:

- raw token returned once and absent from DB/logs;
- correct, missing, malformed, and incorrect credentials tested;
- expired session behavior uses fixed-clock tests;
- PostgreSQL/http integration tests pass.

### Issue #3 — Append-only Session Events and transaction proofs

Deliver:

- Session Event migration and named sqlc queries;
- a database constraint and typed Go constants for exactly the seven action verbs in
  section 4.2;
- application-owned transaction port/adapter;
- SQL proof of atomic state-plus-event write and rollback;
- safe event metadata policy.

Acceptance:

- no partial update/event under forced failure;
- events cannot contain full Personal Details;
- adapter does not leak sqlc/pgx types inward.

### Issue #4 — Verification Session state machine

Deliver:

- domain state and transition rules;
- terminal-state and expiry rules;
- table-driven domain tests;
- database guarded-update query proving stale/concurrent transitions fail.

Acceptance:

- every allowed and forbidden transition has a test;
- domain rules run without infrastructure;
- persistence guard prevents lost updates.

### Issue #5 — Map SQL session paths into the Go HTTP application

Deliver:

- complete `net/http` adapter, middleware, JSON and error helpers;
- PostgreSQL adapter backed by sqlc;
- composition in `cmd/api`;
- issue #1–#5 integration route coverage.

Acceptance:

- error envelope/statuses match compatibility contract;
- unknown JSON fields and multiple JSON objects are rejected;
- context cancellation and server timeouts are respected;
- package dependency direction is clean.

### Issue #6 — Submit immutable Personal Details

Before coding, extract exact request/response fields and edge semantics from the old
issue #6 tests and record them in the new issue document.

Deliver:

- Personal Details migration/queries;
- hybrid HTTP validation;
- atomic insert + state transition + event;
- `submit_personal_details` as the event type for the successful first submission;
- identical and conflicting replay logic;
- no PII duplicated into events.

Acceptance:

- first request, identical replay, different replay, wrong state, wrong token, and
  expiry behavior tested;
- concurrent different submissions cannot both succeed;
- API compatibility for issues #1–#6 is frozen in tests.

After issue #6, run the full current quality gate and create a compatibility report.
Do not begin issue #7 while a core parity test or SQL proof is failing.

### Issue #7 — Identity Document upload and deterministic local validation

Phase A:

- MinIO/AWS SDK v2 adapter;
- Upload Intent schema and presigned URL route;
- superseding, TTL, `HeadObject`, file constraints;
- Verification Artifact schema;
- atomic confirm behavior.
- `confirm_identity_document` for both accepted and bounded local-validation outcomes,
  with the outcome distinguished only by safe metadata;

Phase B:

- deterministic fake DocumentExtractor;
- reconciliation with immutable Personal Details;
- `LOCAL_VALIDATION_FAILED` behavior and safe audit event;
- confirm concurrency and replay protection.

Acceptance includes real MinIO integration and all race/idempotency cases in sections
9 and 14.

**Learning checkpoint:** the user writes the first concept-bearing Upload Intent
migration and its concurrency/constraint proof. The agent reviews it critically,
explains PostgreSQL behavior, and then may implement repetitive queries/adapters. The
user also writes the first application test describing successful confirmation before
the agent fills sibling cases.

### Issue #8 — Biometric Capture upload and submission readiness

Deliver:

- reuse the upload flow for `biometric_capture` with its own media/size rules;
- no document extraction;
- accepted Biometric Verification Artifact;
- transition to `biometric_capture_uploaded`;
- `confirm_biometric_capture` as the successful confirmation event type;
- guard proving both required Verification Artifacts are present before submission.

Acceptance:

- kind-specific validation cannot be bypassed;
- identity and biometric storage keys cannot collide;
- replays/concurrency remain idempotent;
- readiness derives from accepted records, not merely uploaded objects.

**Learning checkpoint:** the user implements the first kind-specific domain rule or
adapter operation. The agent reviews the abstraction created in issue #7 before
generalizing it; do not generalize only because two handlers look similar.

### Issue #9 — Async provider submission and signed verdict callback

Deliver:

- Redis/Asynq adapter and worker process;
- outbox schema, relay, stable task and idempotency IDs;
- submit route returning `202` after DB commit;
- `submit_session`, `verification_passed`, and `verification_failed` for submission
  and applied provider-verdict Session Events;
- fake provider service and scenario endpoint;
- signed webhook verification, deduplication, applied/ignored processing;
- verified/rejected transitions and bounded reasons;
- retry and failure behavior.

Acceptance:

- Redis/provider outage after submit cannot lose durable work;
- relay/task restart and duplicate delivery are safe;
- invalid signatures store nothing;
- duplicate and late callbacks return `200` without extra transition;
- full successful and rejected E2E scenarios pass.

**Learning checkpoint:** the user writes the first outbox migration/query and a proof
of atomic session-event-outbox commit. The user also writes the first webhook HMAC
test. The agent reviews both before implementing the relay and remaining callback
cases.

### Issue #10 — Expiry, cleanup, audit, OpenAPI, and operable MVP

Deliver:

- lazy and scheduled expiry;
- `expire` as the sole expiry Session Event type;
- orphan cleanup task and manual command;
- redacted audit CLI;
- `/health/live` and `/health/ready`;
- `docs/openapi.yaml` with issue #1–#10 contracts;
- request/task structured logging;
- Docker images, environment contract, Compose full topology, and deployment notes;
- CI quality gate and final E2E suite.

Production deployment itself remains deferred.

Acceptance:

- one expiry outcome/event regardless of lazy/scheduled race;
- confirmed Verification Artifacts are excluded from cleanup by query and test;
- OpenAPI examples/status/error envelopes agree with integration tests;
- fresh clone can bootstrap and run the full demo from documented commands;
- security limitations are visible in README and architecture docs.

**Learning checkpoint:** the user writes the first expiry race test or cleanup-safety
proof and reviews the OpenAPI contract. The agent completes repetitive endpoint/schema
documentation only after that review.

## 17. Agent Collaboration Protocol

### 17.1 Issues #1–#6

The implementation agent may build these end to end without pausing for routine
choices. It must still:

- inspect and record the old observable contract before translating each route;
- surface contradictions instead of guessing;
- keep SQL exercises/proofs visible;
- run real verification and report failures honestly;
- stop if a choice would intentionally break compatibility.

### 17.2 Issues #7–#10

These are cooperative learning issues. At each checkpoint:

1. Explain the concept and define the smallest deliverable the user will write.
2. Let the user write the first migration/query/proof/test/adapter operation named in
   the issue.
3. Review it critically, leading with correctness or concurrency risks.
4. Ask the user to revise concept-bearing mistakes; do not silently replace the whole
   deliverable.
5. Once the fundamental is demonstrated, implement sibling/repetitive work.
6. End with integration verification and a learning note.

Do not advance while the core proof or test is red. External framework adoption is a
note for later evaluation, not a milestone.

## 18. New Repository Bootstrap Checklist

An agent beginning in another repository should execute this order:

1. Confirm the repository path, Git remote, and derived module path.
2. Confirm Go `1.26.5`, Docker, PostgreSQL client, sqlc, Goose, and staticcheck
   availability; pin tool versions in documented setup.
3. Initialize the module and create the package tree without speculative interfaces.
4. Add `.gitignore`, `.env.example`, typed config, slog, graceful shutdown, and
   dependency health primitives.
5. Add Compose PostgreSQL/Redis/MinIO with named development volumes and explicit
   health checks.
6. Create `CONTEXT.md` from the glossary/invariants in this plan.
7. Create ADRs for:
   - SQL-first pgx/sqlc and forward-only Goose migrations;
   - pragmatic Clean Architecture and interface policy;
   - direct-to-S3 upload with Upload Intent/Verification Artifact distinction;
   - transactional outbox with Redis/Asynq;
   - valid-signed webhook persistence and idempotency.
8. Create issue documents #1–#10 using section 16 as their contract.
9. Implement issue #1, then progress strictly in order.
10. After issue #6, produce a route-by-route compatibility matrix before issue #7.
11. Apply the learning checkpoints for issues #7–#10.
12. At issue #10, prove a fresh clone/bootstrap/full E2E run.

Do not copy `.env`, credentials, old generated migrations, node dependencies, Drizzle
schema, or TypeScript infrastructure into the new project.

## 19. Implementation Evidence for Handoff

**Verification Artifact** remains reserved for accepted identity or biometric
evidence. Use **implementation evidence** for project handoff material so the two
concepts cannot be confused.

For every issue, retain this implementation evidence:

- migration and SQL proof output;
- generated-code diff check;
- focused unit/application test output;
- integration/E2E test output where applicable;
- request/response examples for changed routes;
- learning note and unresolved limitations;
- exact commands run and whether dependencies were real containers or fakes.

An issue is not done because code compiles. It is done when its observable behavior,
database invariant, failure behavior, and idempotency/concurrency obligations are
proven at the appropriate layer.

## 20. Final Definition of Done

The Go rebuild through issue #10 is complete when:

- it is an independent repository and uses only `lawang_db_go` for local development;
- issues #1–#6 match the established API behavior;
- issues #7–#10 meet every contract in this plan;
- dependency direction is adapters -> application -> domain;
- raw SQL is authoritative and sqlc output is reproducible;
- direct uploads create Verification Artifacts only after confirmation/validation;
- async submission survives Redis/provider failure through the outbox;
- callbacks are signed, deduplicated, and state-guarded;
- expiry and cleanup are safe and idempotent;
- logs/events minimize sensitive data;
- all required quality, integration, and E2E checks pass without silent skips;
- a fresh agent can run the documented bootstrap and demo without relying on this
  TypeScript repository or this conversation;
- limitations and deferred production controls are explicit.
