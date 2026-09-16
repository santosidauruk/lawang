# Checkpoint 3 — Safe Orphan Selection dan Manual Cleanup

Status: menunggu Checkpoint 2 selesai dan gate `[review]` lulus.

## Tujuan

Membuktikan melalui production query dan user-authored SQL proof bahwa cleanup tidak
dapat memilih confirmed intent atau object yang direferensikan Verification Artifact,
lalu menyediakan manual command yang menghapus kandidat aman secara idempotent.

## Istilah baru

- `structural exclusion` adalah predicate SQL yang membuat protected row tidak dapat
  masuk candidate set, bukan pemeriksaan best-effort setelah row terpilih.
- `logical transition time` adalah waktu ketika status secara domain sudah berubah
  walaupun row baru dimaterialisasi oleh pekerjaan yang berjalan kemudian.
- `crash window` adalah rentang setelah external effect berhasil tetapi sebelum
  durable marker berhasil disimpan.
- `object-not-found idempotency` adalah perlakuan bahwa object yang sudah tidak ada
  setara dengan delete yang telah berhasil.

## Keputusan gap Upload Intent expiry

Current application menolak confirmation ketika `now >= upload_intents.expires_at`
tetapi belum menyimpan status `expired`. Cleanup lebih dulu mematerialisasi pending
intent tersebut menjadi `expired` dengan `latest_status_change_at = expires_at`, lalu
candidate query menerapkan grace period dari logical transition time tersebut.

Ini mempertahankan invariant bahwa candidate selection hanya menerima status
`superseded|expired|validation_failed`; pending row tidak boleh langsung dikirim ke
object storage deletion.

## Perilaku tracer

- pending intent yang baru overdue berubah menjadi `expired` tetapi belum menjadi
  cleanup candidate sebelum 24 jam;
- `superseded`, `expired`, dan `validation_failed` yang melewati grace period dapat
  dipilih bila `object_deleted_at IS NULL`;
- confirmed intent tidak pernah terpilih;
- setiap row yang direferensikan Verification Artifact tidak pernah terpilih walaupun
  fixture memaksa status yang tampak cleanup-eligible;
- object deletion terjadi di luar PostgreSQL transaction;
- successful delete atau object-not-found diikuti guarded `object_deleted_at` update;
- transient delete failure meninggalkan marker `NULL` agar retry tetap mungkin.

## Candidate files

- `sql/queries/upload_intents.sql`
- `sql/proofs/009_safe_orphan_cleanup.sql`
- `internal/application/cleanup/service.go`
- `internal/adapter/postgres/cleanup.go`
- `internal/adapter/s3storage/delete.go`
- `cmd/cleanup-orphans/main.go`
- focused PostgreSQL dan MinIO integration tests

## Bagian user

Agent lebih dulu menyiapkan semantic SQL fixtures, fake object storage port, manual
command runner, dan RED real-MinIO tracer tanpa mengisi production selection/delete
behavior.

1. `[sql query]` Mulai dengan query yang mencari pending Upload Intents saat
   `expires_at <= now`. Ubah hanya row tersebut menjadi `expired`, lalu isi
   `latest_status_change_at` memakai nilai `expires_at` milik row agar waktu domain
   expiry tidak bergeser ke waktu cleanup command dijalankan.
2. `[sql query]` Buat query terpisah untuk mengambil kandidat dalam jumlah maksimum
   yang diberikan caller. Batasi status ke `superseded`, `expired`, atau
   `validation_failed`, minta logical transition time sudah lewat 24 jam, dan pilih
   hanya row dengan `object_deleted_at IS NULL`.
3. `[sql query]` Tambahkan structural exclusion langsung ke candidate query dengan
   `NOT EXISTS` terhadap Verification Artifact. Hubungkan exclusion memakai Upload
   Intent/storage ownership yang benar sehingga object tetap terlindungi walaupun
   fixture memiliki status yang tampak eligible untuk cleanup.
4. `[sql query]` Tulis named guarded update untuk merekam deletion time. Query harus
   menerima exact intent ID, hanya mengubah row yang masih cleanup-eligible dan belum
   memiliki marker, serta tidak mengubah status atau metadata lain.
5. `[verification]` Jalankan `make sqlc-generate` setelah keempat handwritten queries
   selesai. Periksa generated methods satu per satu—parameter waktu, limit, result
   columns, dan affected-row return—lalu perbaiki SQL sumber bila bentuk generated
   code belum sesuai.
6. `[postgres adapter]` Implementasikan tiga kelompok operasi adapter secara jelas:
   materialize overdue intents, list safe candidates, dan mark deleted. Map generated
   rows ke application-owned candidate types, periksa affected-row count, dan jangan
   meletakkan S3 call di adapter PostgreSQL ini.
7. `[sql proof]` Susun fixture kecil untuk row sebelum, tepat pada, dan sesudah grace
   boundary. Tambahkan ketiga eligible statuses, confirmed intent, artifact-backed
   row, dan already-deleted row; jalankan production selection SQL lalu assert hanya
   kandidat aman yang terpilih.
8. `[application service]` Tulis use case dengan urutan yang mudah terlihat: materialize
   overdue intents, minta bounded candidates, lalu proses kandidat satu per satu.
   Untuk setiap kandidat, panggil object deletion di luar transaction dan baru
   simpan `object_deleted_at` setelah deletion dinyatakan berhasil.
9. `[application service]` Terjemahkan object-not-found menjadi recorded success
   karena hasil akhirnya sudah sesuai tujuan cleanup. Untuk transient storage
   failure, hentikan atau catat kegagalan sesuai result contract dan kembalikan error
   retryable tanpa menulis deletion marker.
10. `[object storage adapter]` Tambahkan method yang memanggil S3-compatible
    `DeleteObject` untuk satu storage key dari application candidate. Map response
    success, not-found, transient network/service error, dan permanent error ke
    application-owned outcomes tanpa mengembalikan raw SDK message.
11. `[runtime wiring]` Buat `cmd/cleanup-orphans` yang memuat config sekali, membuat
    PostgreSQL pool serta S3 client, menyusun cleanup service, dan menjalankannya
    dengan bounded context. Log hanya safe summary dan keluar non-zero bila use case
    gagal.
12. `[runtime wiring]` Tambahkan target `make cleanup-orphans` sebagai pembungkus
    transparan untuk `go run ./cmd/cleanup-orphans`. Gunakan environment contract yang
    sama dengan binary dan jangan memindahkan credentials ke command-line arguments.
13. `[verification]` Jalankan SQL proof terlebih dahulu agar selection safety terbukti
    sebelum object dihapus. Setelah proof GREEN, jalankan focused application,
    PostgreSQL, dan real-MinIO manual cleanup tracers; simpan command, exit code,
    selected IDs, serta durable deletion-marker assertions.
14. `[review]` Berhenti setelah production queries, user-authored proof, service,
    adapters, dan manual command GREEN. Serahkan diff serta seluruh evidence kepada
    agent; jangan mulai scheduled cleanup atau menambah sibling matrix sebelum review
    selesai.

Kesalahan protected-object selection, grace boundary, external-I/O transaction
scope, atau deletion-marker ordering adalah concept-bearing dan dikembalikan kepada
user.

## Review agent

Agent memeriksa:

- pending intent dimaterialisasi sebelum selection dan tidak dipilih langsung;
- grace period dihitung dari exact logical transition time;
- Verification Artifact exclusion berada dalam production query;
- candidate query memilih explicit columns dan bounded count;
- delete berjalan di luar transaction PostgreSQL;
- marker hanya ditulis setelah success atau object-not-found;
- crash setelah delete tetapi sebelum marker aman direplay;
- command tidak mencetak storage key, SDK error, atau credentials.

## Bagian agent

Setelah cleanup-safety SQL proof dan gate `[review]` lulus, agent menambahkan:

1. application unit siblings untuk empty batch, partial batch, context cancellation,
   dan marker failure;
2. PostgreSQL integration matrix untuk all statuses, exact grace boundary, corrupted
   artifact-backed fixture, concurrency, dan affected-row stale outcome;
3. real-MinIO success, object-not-found replay, transient failure, dan crash-window
   recovery;
4. CLI invalid config, bounded timeout, safe stdout/stderr, and exit-code contracts;
5. focused race detector, SQLC diff, migration validation, vet, dan
   `git diff --check`.

## Stop point

Checkpoint selesai ketika manual command memakai production use case yang sama dengan
tracer, protected objects terbukti tidak selectable, dan all focused gates GREEN.
Scheduled cleanup tetap milik Checkpoint 4.
