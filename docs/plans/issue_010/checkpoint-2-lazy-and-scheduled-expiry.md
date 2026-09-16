# Checkpoint 2 — Lazy dan Scheduled Expiry Convergence

Status: menunggu Checkpoint 1 selesai dan gate `[review]` lulus.

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

## Perilaku tracer

- authentication gagal tidak pernah memicu expiry mutation;
- authenticated overdue request mengembalikan existing public
  `410 SESSION_EXPIRED` dan mempersist expiry melalui operation Checkpoint 1;
- resume, Personal Details, create upload URL, confirm artifact, dan provider submit
  memakai behavior yang sama;
- scheduled sweep memilih due IDs dalam bounded batch lalu memanggil operation yang
  sama satu per satu;
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
   reader. Iterasikan IDs tersebut satu per satu dan panggil exact expiry operation
   Checkpoint 1; tentukan result batch dari jumlah processed, expired, no-op, dan
   failure tanpa menyalin expiry rule.
10. `[queue adapter]` Definisikan task type `session:expire` beserta decoder strict
    untuk payload yang minimum. Handler cukup menerjemahkan Asynq task menjadi
    pemanggilan sweep service dan menerjemahkan hasil/error kembali ke queue; Asynq
    types tidak boleh masuk ke application package.
11. `[queue adapter]` Ubah server registration agar expiry handler dan existing
    provider handler didaftarkan pada satu serve mux. Kerjakan hanya routing dan
    retry mapping di layer ini, lalu pastikan `provider:submit` tetap memakai retry
    count dan backoff yang sudah dibekukan Issue 009.
12. `[runtime wiring]` Tambahkan environment-backed interval dan batch-size config
    untuk scheduled expiry. Parse nilainya sekali saat startup, tolak zero/negative
    values, dan sediakan development defaults yang eksplisit serta mudah dilihat.
13. `[runtime wiring]` Di `cmd/worker`, bangun reader, expiry operation, sweep service,
    queue handler, dan scheduler sesuai dependency direction. Atur lifecycle agar
    scheduler berhenti membuat task lebih dulu, lalu task server menyelesaikan
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
