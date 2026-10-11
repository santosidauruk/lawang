# Checkpoint 2 — Lazy dan Scheduled Expiry Convergence

Status: selesai pada 2026-09-25; Bagian user dan review gate lulus, lalu focused
application, PostgreSQL, Redis, late-callback, build, SQLC, migration, Compose, dan
`git diff --check` verification GREEN.

## Tujuan

Menghubungkan expiry operation Checkpoint 1 ke seluruh applicant operation yang dapat
menemukan session overdue dan ke scheduled Asynq sweep. Kedua jalur harus converge
ke satu durable state `expired` dan satu event `expire`.

## Istilah baru

- `bounded batch` adalah jumlah maksimum record yang diproses dalam satu sweep agar
  satu task tidak mengambil pekerjaan tanpa batas.
- `scheduler` adalah komponen yang membuat task berkala, bukan komponen yang memiliki
  expiry business rule.
- `identifier-only payload` adalah task payload yang hanya membawa identifier yang
  diperlukan dan tidak membawa token, PII, URL, atau object data.
- `aggregate error` adalah satu error ringkas yang menyatakan satu sweep selesai
  dengan sebagian kegagalan tanpa menggabungkan seluruh raw database error ke task.
- `goroutine fan-out` adalah pola membuat banyak goroutine dari satu batch agar item
  diproses bersamaan; Checkpoint ini membatasinya dengan worker pool tetap agar satu
  sweep tidak membuat goroutine sejumlah seluruh item.
- `uniqueness TTL` adalah jendela waktu Redis ketika Asynq menolak task lain dengan
  kombinasi type, payload, dan queue yang sama sebagai duplikat.

## Perilaku tracer

- authentication gagal tidak pernah memicu expiry mutation;
- authenticated overdue request mengembalikan existing public
  `410 SESSION_EXPIRED` dan mempersist expiry melalui operation Checkpoint 1;
- resume, Personal Details, create upload URL, confirm artifact, dan provider submit
  memakai behavior yang sama;
- scheduled sweep memilih due IDs dalam bounded batch lalu memanggil operation yang
  sama melalui worker pool dengan concurrency maksimum 10;
- lazy request dan scheduled task yang race menghasilkan satu state/event;
- valid signed callback sesudah expiry tetap tersimpan sebagai
  `ignored:terminal_session`.

## Candidate files

- existing application services di `internal/application/session/`,
  `personaldetails/`, `artifact/`, dan `providersubmission/`
- `internal/application/expiry/sweep.go`
- `sql/queries/verification_sessions.sql`
- `internal/adapter/postgres/expiry_transactions.go`
- `internal/adapter/queue/session_expiry.go`
- `internal/adapter/queue/server.go`
- `cmd/api/main.go`
- `cmd/worker/main.go`
- focused HTTP/PostgreSQL/Redis tests

## Scaffold agent sebelum Bagian user

- lima applicant service menerima dependency eksplisit `session.ExpiryOperation`;
  legacy constructor call memakai `nil` sampai Bagian user menyambungkan behavior,
  sedangkan runtime production baru disambungkan pada poin 14;
- tracer resume memakai token valid dan exact applicant deadline, mengharapkan
  expiry operation dipanggil sekali sebelum public `410 SESSION_EXPIRED`; sibling
  token-invalid membuktikan operation tidak dipanggil sebelum authentication lulus;
- tracer sweep meminta satu batch berisi dua due session ID, membuktikan batch limit
  dan application time diteruskan ke reader, lalu mengharapkan exact operation
  Checkpoint 1 dipanggil satu per ID setelah reader selesai;
- matrix seluruh applicant route, PostgreSQL race, Redis handler, scheduler, dan
  failure/cancellation tetap Bagian agent setelah focused GREEN serta gate
  `[review]`.

Mulai dari RED lazy resume:

```sh
GOCACHE=/tmp/lawang-go-build go test ./internal/application/session \
  -run 'TestResume(RejectsWrongCredentialWithPublicErrorCode|RejectsExpiredSessionUsingInjectedClock)$' \
  -count=1 -v
```

Expected RED awal adalah token-invalid PASS, lalu authenticated-overdue gagal dengan
`expiry operation calls = 0, want 1 after authentication`.

Setelah resume GREEN, lanjutkan RED scheduled sweep:

```sh
GOCACHE=/tmp/lawang-go-build go test ./internal/application/expiry \
  -run '^TestSweepUsesOneBoundedDueBatchAndTheGuardedExpiryOperation$' -count=1 -v
```

Expected RED awal adalah `session expiry sweep is not implemented` dari placeholder
yang sengaja belum memiliki orchestration. Jangan membuat tracer GREEN dengan scan
atau state-update kedua di sweep; implementasi harus memanggil operasi guarded
expiry Checkpoint 1.

## Keputusan kontrak Checkpoint 2 yang dibekukan

Keputusan berikut disetujui pada 2026-09-20. Implementasi tidak boleh memilih
semantik lain tanpa membuka kembali keputusan yang terkait.

### Poin 1-5 dan 14 — lazy expiry dan API ownership

1. Error dari expiry operation dipetakan sebagai internal failure; caller hanya
   mengembalikan public `SESSION_EXPIRED` setelah operation berhasil memberikan
   durable outcome. Caller tidak boleh menutupi rollback atau database error sebagai
   expiry yang berhasil.
2. `cmd/api` membuat tepat satu `expiry.Service` per API process dari PostgreSQL
   transactor dan application clock process tersebut. Instance operation yang sama
   diberikan kepada session resume, Personal Details, Upload Intent, artifact
   confirm, dan provider submit; masing-masing service tidak boleh membuat expiry
   service atau adapter sendiri.

### Poin 6-8 — due-session query dan PostgreSQL reader

1. Query due-session didukung dua partial B-tree index: `(expires_at, id)` pada
   applicant stages dan `(verification_deadline_at, id)` pada
   `verification_pending` dengan deadline non-null.
2. Migration dan `EXPLAIN` proof harus direview. PostgreSQL tetap boleh memilih
   sequential scan untuk fixture kecil; test tidak boleh memaksa satu plan node tanpa
   representative data.

### Poin 9 — sweep application service

1. `SweepResult` memenuhi invariant
   `Processed = Expired + NoOp + Failed`. `Expired` berarti guarded expiry berhasil
   dengan `Changed=true`; `NoOp` berarti session sudah `expired` atau terminal
   `verified|rejected` memenangkan race; error lain dihitung `Failed`.
2. Kegagalan reader menghentikan sweep dengan result nol dan error. Batch kosong
   mengembalikan result nol tanpa error.
3. Kegagalan satu session tidak menghentikan session berikutnya. Setelah seluruh
   item yang dapat diproses selesai, `Failed > 0` mengembalikan partial result dan
   satu bounded aggregate error agar task tercatat gagal tanpa menyimpan daftar raw
   error yang tidak terbatas. Session yang gagal tetap due di PostgreSQL sehingga
   occurrence berikutnya dapat memilihnya lagi.
4. Cancellation atau deadline context menghentikan dispatch pekerjaan baru dan
   mengembalikan partial result beserta context error; worker yang sudah aktif
   ditunggu selesai. Cancellation bukan ordinary per-session failure yang dilewati.
5. Satu snapshot application clock dipakai untuk pemilihan batch. Guarded expiry
   operation tetap membaca clock-nya sendiri ketika memvalidasi masing-masing
   session agar task tidak membawa deadline snapshot.
6. IDs diproses melalui bounded worker pool dengan concurrency maksimum 10. Result
   counting harus race-safe dan satu goroutine per ID tanpa batas worker dilarang.
7. `cmd/worker` membuat tepat satu `expiry.Service` dan satu `SweepService` per
   worker process. Semua expiry task handler dalam process memakai `SweepService`
   yang sama.
8. `SweepService` memiliki satu shared process-wide limiter berkapasitas 10 yang
   dipakai seluruh pemanggilan `Sweep`; overlapping task tidak membuat limiter baru.
   Beberapa worker replica masing-masing memiliki limit 10, dan tidak ada distributed
   singleton atau distributed sweep lock. PostgreSQL guarded transition tetap menjadi
   correctness boundary lintas-process.

### Poin 10-11 — queue task dan server registration

1. Task `session:expire` memakai exact empty JSON object `{}`, strict decoding,
   serta menolak unknown field dan trailing JSON. Handler memanggil shared `Sweep`
   tepat satu kali tanpa drain loop, query, atau business transition di queue layer.
2. Setiap occurrence memakai TaskID yang dibuat Asynq. String `session:expire`
   adalah task type, bukan fixed TaskID.
3. Scheduled expiry memakai `MaxRetry(0)`. Partial/read/cancellation error membuat
   occurrence itu gagal; recovery bisnis datang dari occurrence berikutnya. Retry
   count dan exponential backoff `provider:submit` tetap tidak berubah.
4. Scheduled expiry tidak memakai `Unique(ttl)`. Duplicate atau overlapping task
   harus tetap benar melalui guarded idempotent expiry operation dan shared
   process-wide limiter; Asynq uniqueness lock bukan correctness dependency.

### Poin 12-13 — worker config, scheduling, dan lifecycle

1. Runtime config memakai interval default 5 menit, batch-size default 100, dan
   batch-size maksimum 1000. Interval dan batch-size configurable melalui
   environment; zero/negative atau batch-size di atas maksimum ditolak saat startup.
2. Worker membuat satu immediate expiry task setelah startup berhasil, kemudian
   scheduler membuat occurrence periodik setiap configured interval.
3. `cmd/worker` adalah composition root yang memiliki PostgreSQL pool, clock,
   adapters, expiry service, sweep service beserta limiter, queue handlers, scheduler,
   relay, dan task server. Application service tidak membuat dependency runtime-nya
   sendiri.
4. Shutdown dilakukan berurutan: hentikan scheduler agar tidak membuat task baru,
   hentikan relay ticker, minta Asynq server menyelesaikan task aktif dalam configured
   shutdown timeout, lalu tutup queue clients dan PostgreSQL pool.

Known limitation yang harus dicatat pada learning note: bila sedikitnya satu batch
penuh berisi session yang selalu gagal karena invariant/data rusak, ordering oldest-
first dapat terus memilih IDs yang sama dan menunda session yang lebih baru. Checkpoint
ini belum menambah retry metadata, quarantine, atau cursor fairness; aggregate error
harus membuat kondisi tersebut terlihat secara operasional dan tidak boleh dilaporkan
sebagai sweep sukses.

## Bagian user

Agent lebih dulu menyiapkan constructor scaffolds, authenticated overdue fixtures,
dan RED lazy-versus-scheduled tracer; user tidak menulis test matrix.

1. `[application service]` Mulai dari session resume: setelah session ditemukan,
   verifikasi resume token lebih dulu agar request tanpa hak tidak dapat membuat
   side effect. Bila token valid dan session overdue, panggil expiry operation
   Checkpoint 1, tunggu durable outcome-nya, lalu kembalikan `SESSION_EXPIRED`.
2. `[application service]` Pada Personal Details submit, temukan titik setelah
   authentication berhasil tetapi sebelum detail dibaca atau ditulis. Panggil expiry
   operation pada titik tersebut dan hentikan alur bila session expired; jangan
   menulis ulang state update atau append event di Personal Details service.
3. `[application service]` Pada create Upload Intent, lakukan authentication dan
   expiry check sebelum membuat intent ID, storage key, atau presigned URL. Bila
   session overdue, kembalikan error expiry dan pastikan presigner sama sekali tidak
   dipanggil.
4. `[application service]` Pada artifact confirm, jalankan expiry check setelah token
   valid dan sebelum object-storage atau extractor work dimulai. Bila expiry menang,
   hentikan alur sebelum `HeadObject`, extraction, atau confirmation transaction
   melakukan pekerjaan lebih lanjut.
5. `[application service]` Pada provider submit, panggil expiry operation setelah
   authentication dan sebelum readiness check, pending-state update, Session Event,
   atau outbox insert. Dengan urutan ini, session overdue tidak dapat membuat
   provider work baru.
6. `[sql query]` Tambahkan named query yang hanya mengembalikan due session IDs dalam
   jumlah maksimum yang diberikan caller. Pisahkan predicate-nya dengan jelas:
   applicant stages membandingkan `expires_at`, sedangkan `verification_pending`
   membandingkan non-null `verification_deadline_at`.
7. `[verification]` Jalankan `make sqlc-generate` sesudah query selection selesai.
   Periksa generated method, parameter waktu, limit, dan result ID; bila diff memuat
   perubahan di luar query baru, cari penyebabnya sebelum melanjutkan.
8. `[postgres adapter]` Implementasikan reader yang menjalankan generated due-session
   query dan mengubah hasilnya menjadi slice session IDs. Tutup database call setelah
   IDs terbaca; jangan mempertahankan transaction atau row lock ketika expiry
   operation memproses setiap ID.
9. `[application service]` Tulis sweep service yang meminta satu bounded batch dari
   reader. Proses IDs melalui worker pool dengan concurrency maksimum 10 dan panggil
   exact expiry operation Checkpoint 1; tentukan result batch secara race-safe dari
   jumlah processed, expired, no-op, dan failure tanpa menyalin expiry rule. Gunakan
   shared process-wide limiter milik satu `SweepService`, bukan limiter baru per call.
10. `[queue adapter]` Definisikan task type `session:expire` beserta decoder strict
    untuk payload yang minimum. Handler cukup menerjemahkan Asynq task menjadi
    pemanggilan sweep service dan menerjemahkan hasil/error kembali ke queue; Asynq
    types tidak boleh masuk ke application package. //ini seperti queue/provider_submission
11. `[queue adapter]` Ubah server registration agar expiry handler dan existing
    provider handler didaftarkan pada satu serve mux. Kerjakan hanya routing dan
    retry mapping di layer ini, lalu pastikan `provider:submit` tetap memakai retry
    count dan backoff yang sudah dibekukan Issue 009.
12. `[runtime wiring]` Tambahkan environment-backed interval dan batch-size config
    untuk scheduled expiry. Parse nilainya sekali saat startup, tolak zero/negative
    values, dan sediakan development defaults yang eksplisit serta mudah dilihat.
13. `[runtime wiring]` Di `cmd/worker`, bangun reader, expiry operation, sweep service,
    shared concurrency limiter, queue handler, dan scheduler sesuai dependency
    direction. Buat masing-masing service tepat sekali per process dan atur lifecycle
    agar scheduler berhenti membuat task lebih dulu, lalu task server menyelesaikan
    shutdown dalam timeout yang sudah dikonfigurasi.
14. `[runtime wiring]` Di `cmd/api`, buat satu expiry dependency lalu berikan instance
    yang sama ke session, Personal Details, Upload Intent, artifact confirm, dan
    provider submit services. Perbarui constructor calls secara eksplisit; jangan
    membuat dependency optional variadic atau expiry service kedua.
15. `[verification]` Jalankan focused lazy HTTP tracer terlebih dahulu, lalu
    PostgreSQL concurrency, Redis task, dan late-callback tracers. Periksa raw verbose
    output agar tidak ada suite yang skip, kemudian catat command, exit code, serta
    evidence satu state/event.
16. `[review]` Berhenti setelah seluruh focused tracer GREEN. Serahkan application,
    handwritten query, generated diff, adapters, runtime wiring, dan output aktual
    kepada agent; jangan mulai object cleanup sebelum review selesai.

Kesalahan auth-before-mutation, duplicated expiry logic, external I/O setelah expiry,
atau scheduled selection deadline adalah concept-bearing dan dikembalikan kepada
user.

## Review agent

Agent memeriksa:

- token invalid/unknown session tetap tidak menulis expiry;
- setiap applicant operation memakai operation Checkpoint 1;
- scheduled scan tidak mengunci row selama satu batch diproses;
- task payload tidak membawa deadline snapshot atau sensitive data;
- scheduler dapat membuat ulang task tanpa transactional outbox;
- queue server mempertahankan provider retry/backoff behavior;
- API dan worker shutdown tetap bounded.

## Bagian agent

Setelah gate `[review]` lulus, agent menambahkan:

1. lazy expiry success/error contract untuk seluruh applicant routes;
2. regression bahwa presign, `HeadObject`, extractor, readiness, outbox, dan provider
   I/O tidak berjalan sesudah expiry;
3. lazy-versus-scheduled dan multiple-sweep PostgreSQL race matrix;
4. applicant/pending deadline boundary siblings;
5. valid late callback durable-ignore proof;
6. malformed/permanent expiry task, Redis restart, scheduler restart, cancellation,
   dan safe logging regressions;
7. focused race detector, vet, SQLC diff, Compose validation, dan `git diff --check`.

## Stop point

Checkpoint selesai ketika seluruh lazy routes, scheduled sweep, concurrency outcome,
dan late-callback behavior GREEN. Object deletion tetap milik Checkpoint 3.
