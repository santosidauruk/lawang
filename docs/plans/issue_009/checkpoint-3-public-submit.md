# Checkpoint 3 — Public Submit dan Exact Idempotent Replay

Status: selesai pada 2026-08-26; Bagian user direview dan seluruh public
failure/concurrency/regression matrix Bagian agent GREEN.

## Tujuan

Menyediakan `POST /verification-sessions/{id}/submit` yang mengautentikasi applicant,
menjalankan atomic Provider Submission dari Checkpoint 1, dan mengembalikan `202`
setelah PostgreSQL commit tanpa menunggu Redis atau provider.

(`idempotent replay` adalah pengulangan request logis yang sama yang mengembalikan
hasil sama tanpa state, event, deadline, atau outbox tambahan.)

## Exact public contract

Initial submit dan replay ketika session sudah `verification_pending` sama-sama:

```http
HTTP/1.1 202 Accepted
Content-Type: application/json

{"id":"<session-uuid>","status":"verification_pending"}
```

Response tidak mengekspos `verificationDeadlineAt`, outbox ID, task ID, atau provider
details.

## Candidate files

- `internal/application/providersubmission/service.go`
- `internal/adapter/httpapi/router.go`
- `internal/adapter/httpapi/provider_submission_test.go`
- `internal/adapter/postgres/provider_submission_transactions.go`
- `cmd/api/main.go`
- public HTTP-to-PostgreSQL integration tracer

## Scaffold agent sebelum Bagian user

- `internal/application/providersubmission/service_test.go` membekukan first submit
  dan durable replay tanpa menulis production orchestration;
- `internal/adapter/httpapi/provider_submission_test.go` membekukan exact `202`
  body untuk initial/replay dan menyediakan injectable auth/error fixture untuk
  continuation agent setelah review;
- `tests/integration/provider_submission_http_postgres_test.go` memakai fixture
  semantic Checkpoint 1 dan membuktikan dua request kelak tetap menghasilkan satu
  deadline, event, dan outbox;
- regression/rollback/concurrency Checkpoint 1 sudah ditutup sebelum RED scaffold
  ini diberikan.

Mulai dari RED application slice:

```sh
GOCACHE=/tmp/lawang-go-build go test ./internal/application/providersubmission \
  -run 'TestSubmit(FirstEligible|Pending)' -count=1
```

Expected RED awal adalah `undefined: providersubmission.NewService`. Setelah poin
1-4 GREEN, lanjutkan focused HTTP contract; jangan menjalankan tracer PostgreSQL
sebagai pengganti application RED pertama.

## Bagian user

Agent menulis RED contract test, auth/error fixture, dan PostgreSQL test setup. User
berfokus pada application/handler production path, bukan HTTP table boilerplate.

1. `[application service]` User mendefinisikan consumer-owned transaction port dan
   result type minimum yang membedakan first submission dari durable replay tanpa
   pgx/sqlc/HTTP types.
2. `[application service]` User menulis `Submit` success orchestration: load session,
   verify raw token, reject applicant expiry, lalu meminta transaction operation dari
   Checkpoint 1.
3. `[application service]` User menangani replay result dari guarded transaction
   operation dan mengembalikan summary sama tanpa write, event, deadline, atau outbox
   kedua.
4. `[verification]` User menjalankan agent-provided application tests sampai GREEN
   untuk first submit dan replay saja.
5. `[http handler]` User menambahkan optional consumer interface untuk Provider
   Submission pada `httpapi.NewHandler` tanpa concrete service dependency.
6. `[http handler]` User mendaftarkan exact POST route, mem-parse Bearer token dan
   session UUID, memanggil service, serta menulis exact `202` body.
7. `[verification]` User menjalankan focused HTTP contract dan real PostgreSQL success
   tracer sampai GREEN.
8. `[runtime wiring]` User menyambungkan service/adapter ke `cmd/api` tanpa Redis atau
   provider dependency pada API process.
9. `[review]` User berhenti dan menyerahkan application service, handler, wiring, dan
   outputs sebelum public failure matrix ditambahkan.

Kesalahan replay yang memperbarui deadline atau membuat outbox kedua, serta API yang
menunggu Redis, adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- API success hanya bergantung pada committed PostgreSQL outcome;
- replay dikenali oleh guarded transaction operation dari durable state/outbox
  outcome, termasuk caller yang kalah race, bukan request-memory cache;
- raw token tidak masuk transaction payload, outbox, response, atau log;
- `verification_pending` replay tidak diuji terhadap applicant `expires_at` yang
  sudah tidak menjadi applicable deadline;
- HTTP adapter hanya tahu application-owned interface/result;
- response byte/field contract tepat dan tidak memiliki field tambahan.

## Review gate evidence

Review Bagian user lulus pada 2026-08-26 tanpa temuan concept-bearing. Application
service memakai guarded PostgreSQL transaction untuk first submit dan durable
replay, HTTP adapter hanya bergantung pada consumer-owned interface, exact response
tidak mengekspos `Replayed`, dan `cmd/api` menyambungkan PostgreSQL service tanpa
Redis atau provider client.

Verification GREEN:

- focused application first/replay tests dengan race detector: 2;
- focused exact HTTP initial/replay contract dengan race detector: 3;
- real HTTP-to-PostgreSQL initial/replay tracer dengan race detector: 1;
- seluruh `internal/...` dengan race detector: 297;
- applicant create/resume, Personal Details, dan Identity Document PostgreSQL HTTP
  regressions dengan race detector: 3;
- scoped `go vet`, scoped Staticcheck, `make sqlc-diff`, `make migration-validate`,
  `make compose-validate`, dan `git diff --check`.

Personal Details integration fixture diperbaiki untuk menjalankan migration 00007
yang kini dibutuhkan shared generated session-lock query. Ini fixture regression
agent-owned, bukan defect Provider Submission behavior.

## Bagian agent — selesai

Setelah review lulus, agent menutup:

1. missing/malformed/wrong Authorization dan unknown session;
2. expired applicant session sebelum first submit;
3. missing required artifacts, wrong non-pending state, dan terminal states;
4. malformed UUID, wrong method, request body framing, safe internal errors, serta
   logging redaction;
5. concurrent exact HTTP submissions converge ke satu event/outbox/deadline dan dua
   exact `202` responses;
6. Redis/provider-unavailable test double membuktikan API tidak memanggil keduanya;
7. existing session/artifact HTTP contracts tetap GREEN.

Public code untuk not-ready/illegal-transition error dibekukan dari behavior tests
yang disiapkan agent; jangan membuat generic error framework.

## Definition of done

- user-authored application service, success handler, dan wiring direview;
- initial/replay response exact dan durable effects tidak berulang;
- API tidak memiliki Redis/provider client;
- full HTTP-to-PostgreSQL tracer GREEN;
- focused race tests dan applicant-route regression GREEN.

## Completion evidence

Bagian agent menutup exact public failure matrix untuk missing/malformed/wrong
Authorization, unknown session, applicant expiry sebelum first submit, missing
accepted artifacts, wrong/terminal state, malformed UUID, wrong method, non-empty
request body, body-read failure, serta internal failure. Error mapping memakai
bounded `SUBMISSION_NOT_READY`, existing `ILLEGAL_TRANSITION`, dan safe `INTERNAL`
tanpa raw error/token leakage.

Concurrent public HTTP proof menjalankan dua caller melalui production handler,
application service, PostgreSQL adapter, dan row lock nyata. Keduanya menerima exact
`202`, sementara database menyimpan tepat satu deadline, `submit_session`, dan
`provider:submit` outbox. Real success tracer hanya menyediakan PostgreSQL; tidak ada
Redis/provider dependency yang tersedia untuk ditunggu. Architecture regression
juga menolak queue/provider imports pada application, HTTP adapter, dan `cmd/api`.

Verification GREEN pada 2026-08-26:

- focused application/HTTP/architecture race suite;
- 13 focused Provider Submission PostgreSQL/HTTP race tests;
- applicant create/resume, Personal Details, dan Identity Document PostgreSQL HTTP
  regressions;
- full `make quality`, termasuk `go test -race ./...` dengan integration suite
  selesai dalam 578.785 detik;
- `go vet`, Staticcheck, `make sqlc-diff`, `make migration-validate`,
  `make compose-validate`, dan `git diff --check`.

Fixture regressions yang ditutup selama checkpoint: artifact dan Personal Details
PostgreSQL helpers kini memasang migration 00007 untuk shared generated session-lock
query; dua unused Provider Submission storage-key helpers dihapus agar Staticcheck
GREEN.
