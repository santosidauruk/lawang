# Checkpoint 4 — Scheduled Cleanup dan Failure Recovery

Status: menunggu Checkpoint 3 selesai dan cleanup-safety gate `[review]` lulus.

## Tujuan

Menjalankan cleanup use case Checkpoint 3 melalui scheduled Asynq task tanpa membuat
selection atau deletion logic kedua, lalu membuktikan retry dan restart recovery.

## Istilah baru

- `transient failure` adalah kegagalan sementara yang layak dicoba ulang karena input
  dan invariant tetap valid.
- `permanent failure` adalah kegagalan yang tidak akan pulih melalui retry dengan
  input yang sama.
- `strict payload` adalah task payload yang menolak unknown field, trailing JSON, dan
  zero-value identifier.
- `recovery matrix` adalah kumpulan skenario failure/restart yang membuktikan durable
  outcome tetap dapat dicapai.

## Perilaku tracer

- scheduled task memanggil exact cleanup service yang dipakai manual command;
- duplicate scheduled tasks aman karena selection mengabaikan marker non-null dan
  object deletion idempotent;
- transient S3 failure diretry;
- malformed task dan permanent application failure tidak diretry;
- Redis/worker restart tidak mengubah protected-object exclusion;
- worker shutdown menunggu active handler secara bounded.

## Candidate files

- `internal/adapter/queue/storage_cleanup.go`
- `internal/adapter/queue/server.go`
- `internal/adapter/queue/logging.go`
- `internal/platform/config/config.go`
- `cmd/worker/main.go`
- focused queue, worker, Redis, PostgreSQL, dan MinIO tests

## Keputusan runtime yang diwarisi dari Checkpoint 2

- Checkpoint ini memperluas composition root, serve mux, scheduler ownership, dan
  shutdown lifecycle worker yang sudah dibuat Checkpoint 2; ia tidak membuat Asynq
  server atau worker process kedua.
- Ketika shutdown, seluruh scheduler termasuk storage cleanup berhenti lebih dulu,
  kemudian relay ticker dihentikan, active handlers didrain secara bounded, dan
  queue clients serta PostgreSQL pool ditutup terakhir.
- Shared limiter concurrency 10 milik expiry sweep hanya membatasi expiry operation;
  cleanup tidak boleh memakai limiter tersebut tanpa keputusan kapasitas terpisah di
  Checkpoint 4.

## Bagian user

Agent lebih dulu menyiapkan strict task decoder tracer, service recorder, dan
worker-restart fixture tanpa mengisi queue/runtime production behavior.

1. `[queue adapter]` Mulai dengan mendefinisikan task type `storage:cleanup` dan exact
   payload shape yang dibutuhkan handler. Gunakan payload sekecil mungkin, terapkan
   strict decode, dan pastikan storage key, candidate rows, credentials, atau
   sensitive config tidak ikut masuk ke Redis.
2. `[queue adapter]` Tulis handler yang decode task, memvalidasi payload, lalu
   memanggil exact cleanup service dari Checkpoint 3. Map malformed payload dan
   permanent application failure menjadi `SkipRetry`; jangan menulis cleanup
   selection logic baru di handler.
3. `[queue adapter]` Bila cleanup service mengembalikan transient failure, teruskan
   error sebagai retryable queue outcome. Bungkus error dengan safe category bila
   perlu, tetapi jangan memasukkan raw S3/SDK message atau object identity ke log.
4. `[queue adapter]` Perluas Asynq serve mux untuk mendaftarkan cleanup, expiry, dan
   provider handlers secara eksplisit. Periksa setiap task type menuju handler yang
   tepat dan jangan mengubah provider retry/backoff ketika menambah route baru.
5. `[queue adapter]` Ubah task completion logger agar tidak lagi bernama atau
   berasumsi provider-only. Untuk semua task types, keluarkan allowlisted fields task
   ID, type, attempt, duration, dan outcome; jangan mencetak payload atau underlying
   error.
6. `[runtime wiring]` Tambahkan cleanup interval dan batch-size ke typed worker config.
   Baca dari environment sekali saat startup, berikan development defaults yang
   jelas, dan tolak nilai zero, negative, atau tidak dapat diparse sebelum dependency
   dibuat.
7. `[runtime wiring]` Buat scheduler yang secara berkala enqueue satu
   `storage:cleanup` task. Gunakan queue client yang sudah dikonfigurasi, perlakukan
   duplicate scheduling secara aman, dan jangan menulis task ini ke transactional
   outbox karena scheduler dapat membuatnya kembali.
8. `[runtime wiring]` Di `cmd/worker`, buat PostgreSQL cleanup store, S3 deletion
   adapter, cleanup service, queue handler, dan scheduler dalam urutan dependency
   yang jelas. Berikan instance service yang sama ke handler; jangan membuat cleanup
   orchestration kedua di composition root.
9. `[runtime wiring]` Atur shutdown berdasarkan arah masuknya pekerjaan: hentikan
   scheduler terlebih dahulu agar task baru tidak dibuat, lalu minta task server
   menyelesaikan active work dalam configured timeout. Setelah itu baru tutup queue,
   database, dan storage clients.
10. `[verification]` Jalankan focused queue decoder dan handler tests lebih dulu,
    kemudian worker lifecycle serta real Redis-MinIO recovery tracer. Periksa verbose
    output untuk retry attempt, eventual marker, safe log fields, dan tidak adanya
    silent skip.
11. `[review]` Berhenti setelah queue, runtime, dan recovery evidence GREEN. Serahkan
    adapter diff, typed config, worker composition, shutdown output, task log sample,
    serta real dependency evidence kepada agent sebelum sibling cases dikerjakan.

Kesalahan retry classification, duplicate task safety, shutdown order, atau
provider-task regression adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- queue adapter tidak memiliki cleanup selection business rule;
- manual dan scheduled path menerima service instance dengan behavior yang sama;
- provider retry count/backoff tetap tidak berubah;
- scheduler tidak memakai transactional outbox;
- duplicate tasks tidak memerlukan exactly-once assumption;
- safe logs tidak mencetak payload, storage key, credentials, atau raw error;
- shutdown order tidak menerima task baru ketika worker sedang drain.

## Bagian agent

Setelah gate `[review]` lulus, agent menambahkan:

1. malformed, unknown-field, trailing-JSON, dan permanent-failure task tests;
2. transient S3 retry dan eventual marker success dengan real Redis/MinIO;
3. duplicate task, concurrent handler, Redis restart, worker restart, dan repeated
   schedule regressions;
4. cancellation saat active delete serta bounded graceful shutdown proof;
5. provider/expiry task registration dan retry-policy non-regression matrix;
6. safe logging assertions dan focused race detector;
7. config negative matrix, vet, Compose validation, dan `git diff --check`.

## Stop point

Checkpoint selesai ketika manual dan scheduled cleanup converge, recovery matrix
GREEN, serta provider/expiry worker behavior tetap lulus. Audit output tetap milik
Checkpoint 5.
