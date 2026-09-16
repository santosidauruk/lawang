# Checkpoint 7 — Provider Submission Task dan Worker Wiring

Status: selesai pada 2026-09-16; user-authored provider task dan worker wiring
melewati gate `[review]`, lalu agent-owned reliability/safety matrix GREEN.

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

1. `[application service]` Definisikan kontrak yang dimiliki application. // bentuk tipe, ambil data prov submision, kirim ke client http di nomor 5 
   Langkah ringkas:
   1. buat tipe input Provider Submission dengan field sesuai exact Checkpoint 6
      shape;
   2. buat reader port yang menerima session ID dan mengembalikan immutable Personal
      Details serta dua accepted Verification Artifacts;
   3. buat provider client port yang menerima request tersebut; dan 
   4. pastikan application package tidak mengimpor SQLC, pgx, Asynq, atau HTTP client
      types.
2. `[sql query]` Tulis query produksi untuk mengambil data immutable. // ngambil yang mau dikirim ke provider
   Langkah ringkas:
   1. tambahkan named query manual di `sql/queries/` untuk satu session ID;
   2. ambil Personal Details yang immutable;
   3. ambil tepat satu accepted `identity_document` dan satu accepted
      `biometric_capture` beserta metadata yang diperlukan provider;
   4. buat urutan hasil deterministic, jangan bergantung pada urutan row PostgreSQL;
      dan
   5. jalankan `sqlc generate`, lalu periksa generated result tanpa mengeditnya.
3. `[postgres adapter]` Bungkus generated query di adapter PostgreSQL. // adapter untuk nomor 2
   Langkah ringkas:
   1. buat reader yang memakai database handle biasa, bukan membuka transaction untuk
      provider I/O;
   2. panggil query dari poin 2 dengan session ID;
   3. map SQLC result ke tipe application dari poin 1;
   4. kembalikan bounded missing-record error bila Personal Details atau salah satu
      artifact tidak ada; dan
   5. jangan mengembalikan SQLC/pgx types keluar adapter.
4. `[application service]` Tulis task service untuk satu provider submission. // ngerjain task di worker
   Langkah ringkas:
   1. terima hanya session ID dari queue handler;
   2. minta reader memuat data terbaru pada saat task dieksekusi;
   3. susun request provider secara deterministic memakai callback URL yang sudah
      divalidasi saat startup;
   4. panggil provider port; dan
   5. jangan mengubah Verification Session atau membuat verdict event di sini.
5. `[provider adapter]` Tulis HTTP client menuju fake provider. // http client dari nomor 1
   Langkah ringkas:
   1. marshal exact JSON tanpa field tambahan;
   2. kirim `Idempotency-Key` dengan exact session UUID;
   3. gunakan request context dan client timeout yang terbatas;
   4. selalu tutup response body dan batasi body yang dibaca; dan
   5. map network/status/response errors ke bounded application error tanpa
      membocorkan body atau data applicant.
6. `[queue adapter]` Tulis decoder dan handler untuk exact `provider:submit` task. // handler buat 4
   Langkah ringkas:
   1. tolak task type selain `provider:submit`;
   2. decode payload secara strict dan izinkan hanya `sessionId`;
   3. tolak UUID kosong/malformed dan trailing JSON;
   4. teruskan hanya parsed session ID ke task service; dan
   5. tahan seluruh Asynq types di dalam queue adapter.
7. `[application service]` Bedakan failure yang boleh di-retry dan yang permanent. // failure handle untuk nomor 1
   Langkah ringkas:
   1. definisikan bounded transient error untuk network/provider sementara;
   2. definisikan bounded permanent error untuk payload, record, atau config lokal
      yang tidak dapat sembuh dengan retry;
   3. pertahankan `context.Canceled` dan `context.DeadlineExceeded`; dan
   4. jangan pernah menerjemahkan integration failure menjadi applicant rejection.
8. `[verification]` Jalankan success tracer pertama sampai GREEN.
   Langkah ringkas:
   1. jalankan focused test scaffold `TestProviderTaskLoadsImmutableRecordsAndSubmitsExactRequest`;
   2. pastikan real Redis menyerahkan task identifier-only;
   3. pastikan provider recorder menerima exact JSON dan exact Idempotency-Key; dan
   4. simpan output GREEN sebelum masuk ke runtime wiring.
9. `[runtime wiring]` Tambahkan typed worker configuration.
   Langkah ringkas:
   1. baca Redis address, password, dan database;
   2. baca provider base URL serta callback URL;
   3. baca webhook secret, concurrency, dan shutdown timeout;
   4. berikan default hanya untuk nilai yang memang aman; dan
   5. gagalkan startup dengan pesan aman bila nilai wajib malformed atau hilang.
10. `[runtime wiring]` Buat composition root `cmd/worker`.
    Langkah ringkas:
    1. buka PostgreSQL dan buat outbox relay;
    2. buat Asynq client/server;
    3. buat PostgreSQL reader, provider HTTP client, dan application task service;
    4. sambungkan semuanya dengan structured logger yang tidak memuat data sensitif;
       dan
    5. pastikan seluruh dependency ditutup oleh satu process lifecycle.
11. `[runtime wiring]` Daftarkan task dan atur lifecycle worker.
    Langkah ringkas:
    1. daftarkan hanya exact `provider:submit` handler untuk Issue 009;
    2. jalankan relay dan Asynq server dari worker process yang sama;
    3. atur sembilan retries setelah initial attempt agar total maksimal sepuluh;
    4. mulai dan hentikan komponen dalam urutan yang aman; dan
    5. jangan menambahkan expiry/cleanup handler milik Issue 010.
12. `[runtime wiring]` Tambahkan Redis dan worker ke local command surface.
    Langkah ringkas:
    1. pin image/version Redis yang disetujui;
    2. tambahkan Redis healthcheck;
    3. tambahkan worker service dan dependency health yang diperlukan;
    4. teruskan hanya environment settings yang dibutuhkan worker; dan
    5. jangan membuka Redis ke public network tanpa kebutuhan lokal yang jelas.
13. `[verification]` Jalankan process-level worker tracer.
    Langkah ringkas:
    1. hidupkan real PostgreSQL, Redis, dan fake-provider;
    2. enqueue satu `provider:submit` task;
    3. buktikan fake-provider menerima satu logical submission dengan stable key;
    4. hentikan worker secara controlled ketika tracer selesai; dan
    5. catat bahwa acknowledgement provider belum mengubah session dari
       `verification_pending`.
14. `[review]` Berhenti dan serahkan hasil untuk review agent.
    Langkah ringkas:
    1. kirim diff reader/query/task/client/queue/worker;
    2. kirim focused GREEN output dan process-level GREEN output;
    3. jangan mulai retry/backoff/exhaustion sibling matrix; dan
    4. tunggu review sebelum meminta agent mengerjakan `Bagian agent`.

Kesalahan outbox snapshot data, missing Idempotency-Key, Asynq type leakage, provider
I/O dalam PostgreSQL transaction, atau exhausted retry yang menolak applicant adalah
concept-bearing dan dikembalikan kepada user.

## Scaffold handoff

Agent menambahkan
[`tests/integration/provider_task_test.go`](../../../tests/integration/provider_task_test.go)
sebelum user flow dimulai. Scaffold tersebut menyediakan:

- disposable PostgreSQL dan Redis;
- immutable Personal Details serta exactly one accepted Identity Document dan
  Biometric Capture fixture;
- provider recorder dengan bounded wait tanpa correctness sleep;
- identifier-only `provider:submit` task dengan sembilan retries setelah initial
  attempt; dan
- executable RED success tracer untuk poin user 1-8.

Satu helper construction, `newFirstProviderTaskHandler`, sengaja mengembalikan
`errCheckpoint7UserWiringRequired`. User hanya mengganti construction helper itu agar
memakai production reader, task service, provider client, dan queue handler. Exact
production types, interface names, query, adapter mapping, dan behavior tetap milik
user. Retry/backoff/exhaustion cases belum ditambahkan sebelum gate `[review]`.

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

## Completion evidence

- user-authored application contract, immutable-record query/reader, provider HTTP
  client, strict queue handler, typed config, worker composition, dan process-level
  tracer melewati gate `[review]`;
- identifier-only task memuat immutable records saat execution dan mengirim exact
  Checkpoint 6 request dengan session UUID sebagai stable `Idempotency-Key`;
- malformed payload dan missing immutable records berhenti sebagai permanent
  failure, sedangkan transient HTTP/network failures tetap retryable;
- `MaxRetry=9` terbukti menghasilkan tepat sepuluh total provider attempts dengan
  deterministic retry delay, tanpa attempt kesebelas;
- retry exhaustion mempertahankan session `verification_pending` tanpa
  `verification_failed`, sedangkan repeated execution dan active-task recovery
  mempertahankan stable idempotency key;
- worker cancellation/restart, safe logging/redaction, dan config negative matrix
  GREEN;
- process-level worker tracer GREEN di bawah race detector; dan
- `make quality` GREEN pada 2026-09-16, termasuk formatting, vet, Staticcheck,
  `go test -race -timeout=20m ./...`, SQLC drift, migration validation, serta Compose
  validation. Integration package selesai dalam 634.078 detik.
