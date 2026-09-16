# Issue 009 — Transactional Outbox dan Signed Webhooks

Status: selesai pada 2026-09-16 setelah delapan checkpoint dan full quality gate
GREEN.

## Boundary yang dipakai

- PostgreSQL menyimpan perubahan `verification_pending`, Session Event, dan outbox
  dalam satu transaction. Response submit tidak menunggu Redis atau provider.
- Outbox UUID adalah stable Asynq TaskID. Verification Session UUID adalah stable
  provider `Idempotency-Key`.
- Worker memuat immutable Personal Details dan dua accepted artifacts saat task
  dieksekusi, kemudian melakukan provider I/O di luar transaction PostgreSQL.
- HMAC-SHA256 diverifikasi terhadap exact raw webhook bytes sebelum JSON parsing,
  logging, atau persistence.
- Webhook Event ID menangani exact replay. Callback berbeda yang datang setelah
  session terminal disimpan sebagai `ignored:terminal_session` tanpa Session Event
  atau terminal transition kedua.

## Dependency aktual

- Docker Desktop client/server `29.6.1`, server API `1.55`.
- Testcontainers Go `v0.39.0`.
- PostgreSQL `postgres:18.4-alpine3.23`.
- Redis `redis:7.4-alpine`.
- MinIO `minio/minio:RELEASE.2024-01-16T16-07-38Z`.
- Asynq `v0.26.0`.

## Evidence dan command aktual

Baseline verified E2E melewati public applicant API, PostgreSQL, real MinIO,
PostgreSQL outbox, real Redis/Asynq, worker, provider HTTP boundary, signed public
webhook route, dan terminal PostgreSQL outcome. Satu trace yang diamati:

- Verification Session: `20c6fc5e-0d83-4c09-8cd0-238e709a4bcc`;
- outbox/Asynq TaskID: `6765d67f-ae85-4207-85d7-cd9efbc9c5b2`;
- provider Idempotency-Key: `20c6fc5e-0d83-4c09-8cd0-238e709a4bcc`;
- Webhook Event: `199ef699-b9b0-49fd-a7c1-97f9f509e776`.

Empat user-owned recovery/replay scenarios dijalankan bersama race detector:

```sh
go test -race -timeout=20m ./tests/integration \
  -run '^(TestRedisRestartAfterDurableSubmitEventuallyProcessesUnpublishedWork|TestProviderAndWorkerRestartRecoverRetryWithStableIdempotencyKey|TestExactDuplicateProviderCallbackReturnsRecordedSuccessWithoutMutation|TestLateProviderCallbackIsDurablyIgnoredWithoutTerminalMutation)$' \
  -count=1 -v
```

Hasil: `4 passed`, `43.553s`. Test menghentikan dan memulai ulang container Redis
nyata, menutup lalu rebind provider listener pada address yang sama, dan mengganti
worker yang memproses retry. Semua wait dibatasi context, ticker, atau observable
event; tidak ada `time.Sleep` sebagai correctness proof.

Late-callback tracer pertama menemukan fixture integration hanya memasang migrasi
sampai `00008`, sehingga callback terminal gagal `500`. Production branch dan
migrasi `00009_allow_terminal_provider_callback_ignore.sql` sudah benar; setelah
fixture memasang migrasi `00009`, duplicate/late race suite GREEN (`2 passed`,
`15.263s`). Karena itu tidak diperlukan perubahan application service pada poin 9.

Agent-owned focused regression matrix mencakup crash sebelum enqueue, expired claim
setelah enqueue sebelum `MarkPublished`, duplicate TaskID, concurrent relay,
worker/provider/API lifecycle, concurrent callback, dan safe logging:

```sh
go test -race -timeout=20m \
  ./tests/integration ./cmd/fake-provider ./cmd/worker \
  ./internal/platform/httpserver ./internal/application/fakeprovider \
  ./internal/adapter/queue ./internal/adapter/httpapi \
  -run '^(TestRedisUnavailableLeavesOutboxRecoverableAfterLease|TestExpiredCrashWindowClaimRequeuesAsOneLogicalTask|TestDuplicateAsynqTaskIDCompletesRelayPublication|TestConcurrentRelayRecordedSuccessAllowsOnlyOneWinner|TestProviderWorkerRestartRecoversActiveTaskWithStableIdempotencyKey|TestFakeProviderProcessDefaultVerifiedSubmissionOnActualListener|TestFakeProviderProcessCanRestartOnSameAddress|TestServeWorkerRunsRelayAndStopsServerOnCancellation|TestRunShutsDownWhenContextIsCancelled|TestServiceConcurrentSameSubmissionStartsOneLogicalCallback|TestServiceShutdownRejectsNewWorkAndWaitsForActiveCallback|TestSafeTaskLoggingOmitsPayloadAndUnderlyingErrors|TestSafeAsynqLoggerDiscardsUntrustedArguments|TestWebhookRequestLoggingExcludesBodySignatureAndSecret)$' \
  -count=1 -v
```

Hasil: `14 passed in 7 packages`. Full rejected E2E juga GREEN untuk exact reasons
`document_invalid`, `biometric_mismatch`, `identity_not_verified`, dan
`suspected_fraud`. Duplicate callback full-loop menghasilkan satu Webhook Event dan
satu terminal Session Event. Invalid signature melalui full harness mengembalikan
`401`, mempertahankan session pada `biometric_capture_uploaded`, dan menyimpan nol
Webhook Event.

Artifact validation dan quality gate final:

```sh
make sqlc-diff migration-validate compose-validate
make quality
```

Keduanya exit `0`. `make quality` menjalankan gofmt check, `go vet`, staticcheck,
seluruh `go test -race -timeout=20m ./...`, SQLC diff, migration validation, dan
Compose validation. Integration package selesai dalam `592.723s`; schema package
dalam `11.333s`. Dependency-backed tests tidak memakai skip ketika Docker tidak
tersedia.

## Recovery semantics dan batas klaim

Redis failure setelah submit tidak menghilangkan row outbox: publish gagal menaikkan
attempt dan row dapat direclaim setelah lease. Crash setelah enqueue tetapi sebelum
`published_at` dapat mengirim ulang logical task; stable TaskID membuat duplicate
enqueue sebagai successful handoff. Provider/network failure memicu retry dengan
stable Idempotency-Key dan tidak mengubah session menjadi rejected.

Ini adalah bukti **at-least-once delivery dengan idempotent effects**, bukan
exactly-once delivery. Crash window tetap dapat menyebabkan task atau callback
dicoba lebih dari sekali. Keselamatan berasal dari stable identifiers, guarded state
transition, Webhook Event deduplication, dan durable ignored outcome. Expiry session
yang tetap pending sesudah retry exhaustion adalah scope Issue 010.
