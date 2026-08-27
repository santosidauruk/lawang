# Checkpoint 7 — Provider Submission Task dan Worker Wiring

Status: menunggu Checkpoint 6; Checkpoint 4 selesai pada 2026-08-27.

## Tujuan

Menjalankan `provider:submit` Asynq task yang hanya membawa session identifier,
memuat immutable Personal Details dan accepted Verification Artifacts saat execution,
lalu memanggil fake provider dengan session UUID sebagai stable `Idempotency-Key`.

(`task handler` adalah operation yang dijalankan worker ketika Asynq menyerahkan satu
task.) (`exponential backoff` adalah penambahan retry delay secara bertahap agar
provider yang sedang gagal tidak terus dibombardir.)

## Perilaku tracer

- task payload hanya memiliki `sessionId`;
- handler memuat records pada execution time, bukan memakai snapshot di outbox;
- provider request mengikuti exact Checkpoint 6 shape;
- session UUID menjadi exact HTTP `Idempotency-Key`;
- transient failure di-retry dengan backoff, maksimal sepuluh total attempts;
- exhausted attempts meninggalkan session `verification_pending` dan tidak menulis
  `verification_failed`;
- successful provider acknowledgement belum mengubah session; verdict hanya masuk
  melalui signed webhook.

## Candidate files

- `internal/application/providersubmission/task_service.go`
- `internal/adapter/postgres/provider_submission_reader.go`
- `internal/adapter/providerhttp/client.go`
- `internal/adapter/queue/asynq.go`
- `cmd/worker/main.go`
- PostgreSQL + Redis + fake-provider integration tests

## Bagian user

Agent menyiapkan task/application RED tests, immutable-record fixtures, disposable
Redis, dan provider recorder. User menulis provider client, task operation, queue
handler, dan seluruh Asynq worker wiring sesuai approval.

1. `[application service]` User mendefinisikan application-owned Provider Submission
   request, immutable-record reader port, dan provider client port.
2. `[postgres adapter]` User menulis query/adapter operations yang memuat immutable
   Personal Details serta exact accepted Identity/Biometric Verification Artifacts
   untuk one session.
3. `[application service]` User menulis task service yang menerima hanya session ID,
   memuat records, membentuk deterministic provider request, lalu memanggil provider
   port.
4. `[provider adapter]` User menulis HTTP client yang mengirim exact JSON,
   `Idempotency-Key: <session-uuid>`, bounded timeouts, dan safe response handling.
5. `[queue adapter]` User menulis Asynq task decoder/handler yang memvalidasi task type
   dan identifier-only payload sebelum memanggil application task service.
6. `[application service]` User mendefinisikan bounded transient/permanent integration
   errors tanpa mengubahnya menjadi applicant rejection.
7. `[verification]` User menjalankan Redis task -> provider recorder success tracer
   sampai GREEN.
8. `[runtime wiring]` User menambahkan Redis/provider/worker settings ke typed config,
   termasuk Redis address/password/database, provider base URL, callback URL,
   webhook secret, concurrency, dan shutdown timeout.
9. `[runtime wiring]` User membuat `cmd/worker` composition yang menyambungkan
   PostgreSQL outbox relay, Asynq client/server, provider task service, provider HTTP
   client, dan safe structured logger dalam satu process.
10. `[runtime wiring]` User mendaftarkan exact `provider:submit` handler serta graceful
    start/shutdown order tanpa menambahkan Issue 010 handlers.
11. `[runtime wiring]` User menambahkan Redis dan worker service ke Compose/local
    command surface dengan pinned image/version dan health behavior yang relevan.
12. `[verification]` User menjalankan real Redis + fake-provider worker tracer serta
    controlled shutdown sampai GREEN.
13. `[review]` User berhenti sebelum retry/backoff/exhaustion sibling matrix.

Kesalahan outbox snapshot data, missing Idempotency-Key, Asynq type leakage, provider
I/O dalam PostgreSQL transaction, atau exhausted retry yang menolak applicant adalah
concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- outbox/task membawa identifier only;
- records dimuat pada execution time dan exactly two accepted artifact kinds ada;
- provider request ordering/fields deterministic;
- HTTP timeouts/cancellation dan body-close/error limits aman;
- Asynq task/API types berhenti di queue adapter;
- worker process memiliki satu lifecycle untuk relay dan handler;
- Asynq retry option dihitung sebagai sembilan retries setelah initial attempt agar
  total provider attempts tidak melebihi sepuluh;
- logs hanya berisi safe task ID/type/attempt/duration/outcome;
- no expiry/cleanup task leakage dari Issue 010.

## Bagian agent

Setelah user worker wiring direview, agent menutup:

1. missing/malformed task payload dan missing immutable records;
2. transient provider status/network errors menghasilkan retry;
3. permanent local payload/config errors tidak spin tanpa batas;
4. exact maximum ten total attempts dan deterministic retry-delay function tests;
5. exhausted attempts mempertahankan session pending tanpa verdict event;
6. same task/session repeated execution mempertahankan stable Idempotency-Key;
7. worker cancellation/restart dan active task recovery;
8. safe logging/redaction, config negative matrix, race suite, dan Compose regression.

## Definition of done

- user-authored reader/task/client/queue handler/worker wiring direview;
- task memuat immutable records saat execution;
- exact session UUID Idempotency-Key terbukti;
- retry maksimal sepuluh dan exhaustion bukan rejection;
- one worker process menjalankan relay serta provider handler;
- real PostgreSQL + Redis + fake-provider suite GREEN.
