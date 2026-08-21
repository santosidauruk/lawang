# Checkpoint 3 — Public Submit dan Exact Idempotent Replay

Status: menunggu review gates Checkpoint 1 dan 2.

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

## Bagian agent

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
