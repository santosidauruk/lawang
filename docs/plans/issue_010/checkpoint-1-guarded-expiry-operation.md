# Checkpoint 1 — Guarded Expiry Operation

Status: aktif; scaffold agent RED siap pada 2026-09-16, lalu Bagian user harus
berhenti pada gate `[review]`.

## Tujuan

Menyediakan satu application-owned operation yang mengubah overdue Verification
Session menjadi `expired` dan menulis tepat satu Session Event `expire` dalam satu
PostgreSQL transaction.

Checkpoint ini membuktikan operasi inti secara langsung. HTTP lazy expiry dan
scheduled Asynq sweep baru disambungkan pada Checkpoint 2.

## Istilah baru

- `guarded update` adalah update yang hanya berhasil bila row masih berada pada state
  dan deadline yang diharapkan.
- `applicable deadline` adalah `expires_at` untuk applicant stage dan
  `verification_deadline_at` untuk state `verification_pending`.
- `fixed clock` adalah sumber waktu test yang nilainya dikendalikan agar boundary
  deadline dapat dibuktikan secara deterministik.
- `one-winner concurrency` adalah kondisi ketika beberapa caller berlomba tetapi
  hanya satu caller yang membuat durable state transition dan event.

## Perilaku tracer

Untuk applicant-stage session dengan `now >= expires_at` atau pending-provider
session dengan `now >= verification_deadline_at`:

- application operation mengunci row session;
- domain `verificationsession.Expire` memvalidasi state dan boundary waktu;
- guarded update mengubah state menjadi `expired` dan `updated_at` menjadi application
  time;
- satu event `expire` dengan empty safe metadata ditulis pada waktu yang sama;
- concurrent atau repeated call tidak menulis state transition maupun event kedua;
- `verified` dan `rejected` tidak pernah berubah menjadi `expired`.

Pending-provider session tanpa `verification_deadline_at` adalah invariant violation;
operation tidak boleh diam-diam kembali memakai applicant `expires_at`.

## Candidate files

- `internal/application/expiry/service.go`
- `sql/queries/verification_sessions.sql`
- `internal/adapter/postgres/expiry_transactions.go`
- focused application test di `internal/application/expiry/`
- focused PostgreSQL tracer di `tests/integration/`

Nama package dapat menyesuaikan domain language yang muncul dari tracer. Jangan
membuat generic unit-of-work framework.

## Scaffold agent sebelum Bagian user

- `internal/application/expiry/service_test.go` membekukan satu fixed-clock
  application behavior untuk session `verification_pending` tepat pada
  `verification_deadline_at`; applicant `expires_at` yang lebih lama mencerminkan
  timeline durable, sedangkan proof no-fallback tetap sibling Bagian agent setelah
  review;
- `tests/integration/expiry_postgres_test.go` menyediakan fixture disposable
  PostgreSQL untuk dua caller serentak terhadap session `created` tepat pada
  `expires_at`, lalu mengassert satu changed result, satu durable state `expired`,
  dan satu safe event `expire` pada application time;
- `internal/application/expiry/doc.go` hanya membuka package boundary dan tidak
  mengisi service, port, result, SQL, atau adapter production milik user.

Mulai dari RED application slice:

```sh
GOCACHE=/tmp/lawang-go-build go test ./internal/application/expiry \
  -run '^TestExpirePendingSessionAtItsProviderDeadline$' -count=1
```

Expected RED awal adalah `undefined: expiry.NewService`, `expiry.Input`, dan
`expiry.Transaction`. Setelah application tracer GREEN, jalankan tracer PostgreSQL:

```sh
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run '^TestConcurrentGuardedExpiryCommitsOneStateAndOneEvent$' -count=1 -v
```

Expected RED PostgreSQL juga menunggu `postgres.NewExpiryTransactions`; jangan
mengubah assertion menjadi raw-SQL ACT untuk melewati application/adapter boundary.

## Bagian user

Agent lebih dulu menyiapkan fixed-clock application tracer dan disposable PostgreSQL
concurrency tracer tanpa mengisi production operation milik user.

1. `[application service]` Mulai dengan membaca input yang dipakai tracer: session ID
   dan waktu dari application clock. Setelah itu buat tipe input, result, transaction
   port, dan transactor port yang hanya berisi operasi yang benar-benar dibutuhkan
   oleh satu expiry attempt; jangan menambahkan interface umum untuk kebutuhan yang
   belum ada.
2. `[application service]` Mulai transaction callback dengan mengambil dan mengunci
   session berdasarkan ID. Setelah session terbaca, pilih `expires_at` untuk semua
   applicant stages atau `verification_deadline_at` untuk `verification_pending`;
   bila deadline pending bernilai `nil`, hentikan operation sebagai invariant
   violation dan jangan melakukan write.
3. `[application service]` Kirim current state, application time, dan applicable
   deadline ke `verificationsession.Expire`. Gunakan hasil domain tersebut untuk
   membedakan session yang memang harus menjadi `expired`, session yang belum due,
   dan terminal/no-op; jangan menyalin aturan transisi state ke application service
   atau adapter.
4. `[sql query]` Buka handwritten query file untuk Verification Session lalu tambahkan
   satu named guarded update. Query harus mencari exact session ID, menerima expected
   state, mengubahnya ke `expired`, menyimpan application time, dan tidak menyentuh
   row bila state sudah berubah karena caller lain.
5. `[verification]` Jalankan `make sqlc-generate` setelah handwritten query selesai.
   Baca generated diff untuk memastikan parameter dan affected-row result sesuai
   kebutuhan; bila hasilnya salah, perbaiki handwritten SQL dan generate ulang,
   jangan mengedit `internal/adapter/postgres/sqlc/` secara manual.
6. `[postgres adapter]` Buat transaction wrapper yang membuka satu PostgreSQL
   transaction dan membuat satu transaction-bound query set. Gunakan query set yang
   sama untuk lock session, guarded update, dan append event, lalu commit hanya bila
   ketiga langkah tersebut berhasil.
7. `[postgres adapter]` Saat memetakan row yang terkunci, ubah nullable
   `verification_deadline_at` menjadi `*time.Time` tanpa panic. Setelah guarded update
   dijalankan, periksa affected-row count dan kembalikan stale concurrent outcome
   bila nilainya bukan tepat satu.
8. `[application service]` Simpan result sementara selama transaction callback
   berjalan, tetapi jangan kembalikan success sebelum transactor menyatakan commit
   berhasil. Bila update, append event, atau commit gagal, kembalikan error dan result
   kosong agar caller tidak menganggap transaction rollback sebagai success.
9. `[verification]` Jalankan focused application tracer terlebih dahulu, lalu
   PostgreSQL tracer yang disiapkan agent. Pastikan keduanya benar-benar GREEN tanpa
   skip, kemudian catat exact command, exit code, dan bagian output yang menunjukkan
   state `expired` serta satu event `expire`.
10. `[review]` Hentikan pekerjaan setelah focused evidence GREEN. Serahkan application
    operation, handwritten query, generated diff, adapter transaction, dan output
    command kepada agent; jangan mulai HTTP, Asynq, atau sibling cases sebelum review
    selesai.

Kesalahan deadline selection, terminal-state mutation, transaction ownership, atau
event duplication adalah concept-bearing dan dikembalikan kepada user untuk direvisi.

## Review agent

Agent memeriksa:

- `now == deadline` sudah dianggap overdue;
- applicant expiry tidak membaca `verification_deadline_at`;
- pending-provider expiry tidak fallback ke `expires_at`;
- domain operation tetap menjadi pemilik aturan terminal/deadline;
- update dan event memakai satu transaction dan satu application timestamp;
- expired replay menjadi no-op, sedangkan verified/rejected tetap terminal;
- generated SQLC hanya berasal dari handwritten query.

## Bagian agent

Setelah gate `[review]` lulus, agent menutup satu behavior per RED → GREEN:

1. not-due applicant dan pending-provider session tidak bermutasi;
2. exact boundary `now == deadline` menghasilkan satu expiry;
3. missing pending deadline menghasilkan safe internal failure tanpa writes;
4. forced update/event failure membuktikan rollback PostgreSQL;
5. two concurrent expiry attempts menghasilkan satu state change dan satu event;
6. repeated expiry, verified, serta rejected calls tidak menambah event;
7. focused race detector, SQLC diff, migration validation, dan `git diff --check`.

## Stop point

Checkpoint selesai ketika focused application/PostgreSQL suites GREEN dan review
ownership lulus. Jangan menyambungkan HTTP, Asynq, atau scheduled scan sebelum
Checkpoint 2 dimulai.
