# Checkpoint 6 — Replay dan Concurrent Confirm

Status: selesai pada 2026-08-11. Concurrent success/mismatch, recorded replay,
advisory-lock lifecycle, stale-result guards, cancellation, create-vs-confirm, dan
PostgreSQL + MinIO replay path sudah dibuktikan dengan race detector.

## Tujuan

Memastikan request ulang dan request confirm bersamaan menghasilkan satu database
outcome, satu event, paling banyak satu Verification Artifact, serta tidak mengulang
`HeadObject` atau extraction.

## Arti session-level advisory lock

`session` pada istilah PostgreSQL ini adalah umur satu koneksi database yang sedang
dipinjam, bukan row atau domain Verification Session.

Alur yang dipilih:

1. pin satu koneksi PostgreSQL dari pool;
2. ambil session-level advisory lock untuk key yang sudah disetujui;
3. baca ulang outcome; bila intent sudah `confirmed` atau `validation_failed`,
   kembalikan recorded outcome tanpa MinIO/extractor;
4. bila masih pending, lakukan `HeadObject` dan extraction tanpa transaction terbuka;
5. buka transaction singkat pada koneksi yang sama, lock/re-read guard, lalu commit
   accepted atau validation-failed outcome;
6. tutup transaction;
7. lepaskan advisory lock dalam `defer`, lalu kembalikan koneksi ke pool.

Advisory lock berfungsi seperti mutex lintas process untuk request yang memakai key
sama. Go `sync.Mutex` saja hanya melindungi satu process. Transaction-level advisory
lock tidak dipakai untuk mencakup external I/O karena itu memaksa transaction tetap
terbuka terlalu lama. Tradeoff strategi terpilih: tidak ada long-running transaction,
tetapi satu pool connection tetap ditempati selama object storage/extraction.

Tidak ada status `confirming`; advisory lock adalah koordinasi sementara dan outcome
durable tetap status Upload Intent yang sudah ada.

## Audit checkout sebelum RED (catatan historis)

Kondisi kode saat workbench disiapkan:

1. `artifact.Service.Confirm` hanya menerima intent `pending`. Intent `confirmed` atau
   `validation_failed` berhenti di `validatePendingIdentityIntent` sebagai
   `CONFIRMATION_STALE`; recorded replay belum ada.
2. `HeadObject` dan extraction sudah berada sebelum transaction singkat. Boundary ini
   harus dipertahankan saat koordinasi ditambahkan.
3. `postgres.ArtifactTransactions` dapat dibangun di atas `pgxpool.Pool`, tetapi
   setiap read/transaction saat ini bebas memakai koneksi berbeda. Belum ada boundary
   yang meminjam satu koneksi, memegang advisory lock, lalu membuka transaction pada
   koneksi yang sama.
4. Database sudah memiliki unique constraint satu artifact per Upload Intent dan satu
   artifact per `(Verification Session, kind)`. Constraint ini adalah defense terakhir,
   bukan pengganti replay outcome dan single external validation.
5. Checkpoint 5 sudah selesai. Public-host presign, direct HTTP PUT, real MinIO
   `HeadObject`, sibling JPEG/PNG/PDF, runtime wiring, dan full
   HTTP -> PostgreSQL -> MinIO -> confirm tracer telah GREEN. Pekerjaan yang tersisa
   untuk Issue 007 adalah replay dan concurrent-confirm hardening di checkpoint ini.

Implikasi RED: implementation sekarang dapat menghasilkan satu sukses dan satu
`CONFIRMATION_STALE`; bila kedua caller overlap sebelum transaction, external counter
juga dapat menjadi dua. Jangan mengunci test pada bentuk RED tertentu karena scheduler
boleh menghasilkan interleaving berbeda. Yang harus RED adalah kontrak publik akhir:
dua recorded success yang sama dan external work tepat sekali.

## Keputusan lock yang dibekukan

Session-level advisory lock memakai **Upload Intent ID** sebagai isolation scope.
Artinya, dua confirm untuk Upload Intent yang sama harus antre, sedangkan intent yang
berbeda tetap boleh diproses bersamaan. Create/supersede tidak memakai lock ini;
transactional re-read yang sudah ada tetap bertugas membuang hasil external I/O bila
intent berubah selama confirm berlangsung.

PostgreSQL advisory lock menerima key 64-bit, bukan UUID 128-bit. Mapping yang dipilih
dan harus tinggal di PostgreSQL adapter adalah:

1. ambil 16 raw bytes dari Upload Intent UUID;
2. hitung SHA-256 dari bytes tersebut;
3. ambil delapan byte pertama hasil hash dalam urutan big-endian;
4. pertahankan bit pattern itu sebagai signed `int64`;
5. pakai nilai yang sama untuk `pg_advisory_lock(bigint)` dan
   `pg_advisory_unlock(bigint)`.

Mapping ini deterministic di semua process, memakai seluruh UUID, tidak bergantung
pada versi fungsi hash internal PostgreSQL, dan tidak mengekspos detail PostgreSQL ke
application package. Karena 128 bit dipetakan menjadi 64 bit, collision tetap mungkin,
tetapi sangat kecil untuk UUID yang dibuat aplikasi. Collision hanya membuat dua
intent yang tidak berkaitan ikut antre; database ownership guard dan unique constraint
tetap menjadi penjaga correctness.

## Seam arsitektur yang perlu muncul dari tracer

Jangan menaruh `pgxpool.Conn` atau SQL advisory lock di application package. Boundary
minimum yang dibutuhkan secara perilaku adalah coordinator yang:

1. menerima context dan Upload Intent ID;
2. meminjam satu dedicated pool connection;
3. memperoleh session-level lock pada connection itu;
4. memberi application scope untuk read dan transaction pada connection yang sama;
5. membiarkan application menjalankan `HeadObject`/extraction ketika lock masih
   dipegang tetapi transaction belum dibuka;
6. unlock lalu release connection pada semua exit path.

Nama interface/constructor belum dibekukan. Aktifkan tracer lebih dahulu dan gunakan
RED dari behavior publik untuk menunjukkan seam terkecil yang dibutuhkan; RED pertama
tidak harus berupa compile failure. Preflight session lookup dan resume-token
authorization harus tetap terjadi sebelum request boleh menunggu advisory lock.
Setelah lock diperoleh, authorization, expiry, intent outcome, dan guard lain tetap
dibaca ulang; preflight tidak boleh dipercaya sebagai commit authority.

Untuk cleanup, `defer conn.Release()` saja belum cukup bila unlock gagal: session-level
lock hidup bersama connection. Adapter harus memastikan connection yang mungkin masih
memegang lock tidak kembali sehat ke pool. Detail ini direview setelah tracer pertama
GREEN, bersama cancellation dan pool-leak tests.

## Workbench dan hasilnya

Workbench user di `tests/integration/artifact_confirm_concurrency_test.go` sudah
diaktifkan menjadi public test. Tracer awal membuktikan dua public `Confirm` untuk
intent yang sama menerima recorded success yang sama, sementara database menyimpan
satu artifact/event dan external work berjalan sekali. Sibling agent kemudian
menambahkan pengamatan `pg_locks` yang menunjukkan satu lock granted dan satu waiter;
jadi hasil hijau tidak bergantung pada kebetulan scheduler menjalankan caller secara
berurutan.

## Bagian user

Status: selesai dan dipertahankan sebagai catatan urutan belajar yang dikerjakan.

Target bagian ini sederhana: dua caller mengonfirmasi Upload Intent yang sama. Keduanya
mendapat success yang sama, tetapi database hanya menyimpan satu outcome dan pekerjaan
external hanya berjalan sekali.

Kerjakan urutan berikut satu per satu:

1. **Pakai keputusan lock di atas.** Lock selalu berdasarkan Upload Intent ID dan
   mapping SHA-256 -> `int64` yang sudah dibekukan. Jangan memilih key atau algoritma
   lain di tengah implementasi.
2. **Aktifkan satu test saja.** Rename
   `checkpoint6ConcurrentConfirmReturnsRecordedSuccessOnce` menjadi
   `TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce`, lalu
   hapus fatal marker. Jangan menambah replay/mismatch/cancellation tests dulu.
3. **Siapkan data PostgreSQL nyata.** Gunakan `openArtifactDatabase`, lalu seed satu
   session `personal_details_submitted`, Personal Details, dan satu pending
   `identity_document` Upload Intent. Buat `pgxpool.Pool` dari connection string dan
   set kapasitasnya minimal dua koneksi. Jangan berbagi satu raw `*pgx.Conn` kepada
   dua goroutine.
4. **Buat fake untuk pekerjaan external.** Fake `HeadObject` mengembalikan JPEG
   non-zero dengan ETag. Fake extractor mengembalikan identity number yang sama dengan
   Personal Details. Hitung pemanggilan keduanya dengan mutex atau atomic agar aman
   dipakai bersamaan. Fake tidak boleh melakukan lock atau menulis database.
5. **Mulai dua confirm bersama.** Siapkan dua goroutine sampai keduanya ready, lalu
   `close(start)` sekali. Keduanya memanggil public `service.Confirm` dengan session,
   token, dan Upload Intent yang sama. Masing-masing harus mengirim tepat satu
   `{summary, err}` ke buffered channel. Tunggu keduanya selesai dengan `WaitGroup`;
   jangan memakai `time.Sleep` untuk memilih winner.
6. **Periksa hasil caller.** Kedua error harus nil. Kedua summary harus memiliki ID,
   status `identity_document_uploaded`, dan expiry yang sama. Caller kedua harus
   menerima success yang dibaca dari database, bukan `CONFIRMATION_STALE`.
7. **Periksa hasil database.** Buktikan Upload Intent menjadi `confirmed`, hanya ada
   satu Verification Artifact, hanya ada satu event `confirm_identity_document`, dan
   session menjadi `identity_document_uploaded`. Jangan menganggap unique constraint
   saja sudah cukup membuktikan behavior.
8. **Periksa jumlah pekerjaan external.** Setelah kedua caller selesai, assert
   `HeadObject` dipanggil sekali dan extractor dipanggil sekali. Start gate dan counter
   ini adalah tracer behavior awal, bukan bukti final bahwa caller kedua benar-benar
   sempat menunggu advisory lock; agent akan menambahkan proof lock yang deterministic
   setelah tracer ini GREEN.
9. **Simpan RED yang benar.** Jalankan focused test pada implementation sekarang.
   Test harus gagal pada kontrak akhirnya tanpa panic, data race, atau deadlock.
   Implementasi sekarang kemungkinan menghasilkan satu success dan satu
   `CONFIRMATION_STALE`; jangan mengubah expected result agar mengikuti behavior lama.
10. **Tambahkan coordinator minimum.** Application meminta satu confirm dijalankan
    secara eksklusif untuk Upload Intent tersebut. PostgreSQL adapter yang meminjam
    dedicated connection, mengambil advisory lock, memberi reader/transaction dari
    connection yang sama, dan melepas semuanya. `pgxpool.Conn` dan SQL lock tidak boleh
    masuk ke application package. `HeadObject` dan extraction tetap berjalan ketika
    lock dipegang tetapi tanpa transaction terbuka.
11. **Tambahkan confirmed replay saja.** Setelah lock diperoleh, baca ulang database.
    Jika intent sudah `confirmed`, bentuk `session.Summary` dari outcome tersimpan dan
    langsung kembalikan success. Jangan memanggil `HeadObject`, extractor, write, atau
    event lagi. Validation-failed replay belum dikerjakan pada siklus user ini.
12. **Bersihkan lock dengan aman.** Setelah acquire sukses, segera daftarkan cleanup.
    Unlock harus terjadi sebelum release connection. Cleanup memakai context terpisah
    yang berbatas waktu agar request context yang sudah canceled tidak menggagalkan
    unlock. Jika unlock gagal, keluarkan connection dari pool dan tutup; jangan
    kembalikan connection yang mungkin masih memegang session-level lock.
13. **Buat GREEN lalu berhenti.** Jalankan focused test dan race variant. Setelah
    keduanya GREEN, berhenti untuk review sebelum agent mengerjakan sibling behavior.

Urutan command user:

```sh
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run '^TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce$' \
  -count=1

GOCACHE=/tmp/lawang-go-build go test -race ./tests/integration \
  -run '^TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce$' \
  -count=1
```

## Review agent

Agent memeriksa lock ownership/lifetime, implementasi mapping SHA-256 -> `int64`,
collision risk, pool berkapasitas minimal dua koneksi, cancellation, unlock pada semua
exit, connection disposal bila unlock gagal, tidak adanya open transaction saat
external I/O, dan apakah test benar-benar menjalankan dua public calls.

## Kelanjutan agent

Setelah concurrent-success tracer GREEN:

1. [x] proof lock yang deterministic menahan caller pertama pada external boundary dan
   mengamati caller kedua menunggu advisory lock yang sama melalui PostgreSQL, tanpa
   `time.Sleep`; proof ini juga memastikan test tidak hijau hanya karena scheduler
   menjalankan kedua caller secara berurutan;
2. [x] confirmed replay mengembalikan recorded success tanpa external work/event;
3. [x] validation-failed replay mengembalikan exact bounded failure tanpa external work;
4. [x] concurrent mismatch menghasilkan satu failure event dan zero artifacts;
5. [x] supersede/expiry saat request menunggu lock menghasilkan conflict yang tepat;
6. [x] stale result setelah external I/O dibuang tanpa partial writes;
7. [x] external failure melepaskan lock sehingga retry dapat berjalan;
8. [x] context cancellation ketika menunggu lock tidak membocorkan connection/lock;
9. [x] race tests untuk create intent dan confirm;
10. [x] full PostgreSQL + MinIO path membuktikan external work tidak terulang.

## Definition of done

- confirmed dan validation-failed replay exact serta side-effect free;
- concurrent success/mismatch hanya melakukan satu external validation dan satu
  durable outcome;
- PostgreSQL proof secara deterministic menunjukkan caller kedua menunggu advisory
  lock yang dipegang caller pertama; start gate atau fake counter saja tidak cukup;
- unique constraints tetap menjadi defense terakhir;
- tidak ada transaction terbuka selama MinIO/extractor I/O;
- advisory lock selalu dilepas dan pool connection dikembalikan;
- `go test -race ./...`, PostgreSQL concurrency tests, MinIO integration tests,
  `sqlc` diff, migration validation, dan full quality gate lulus;
- issue acceptance checklist dan learning note diperbarui dengan output aktual.

Agent tidak boleh menyatakan Issue 007 selesai hanya dari fake counter. Bukti akhir
harus mencakup PostgreSQL nyata dan MinIO nyata.

## Evidence aktual

- Focused Checkpoint 6 integration race suite lulus pada 2026-08-11.
- HTTP -> PostgreSQL -> MinIO tracer menjalankan confirm dan replay dengan response
  identik; counting wrapper membuktikan real `HeadObject` dan deterministic extraction
  masing-masing hanya sekali.
- Unit test coordinator membuktikan unlock sebelum release, discard saat acquire atau
  unlock gagal, serta cleanup dengan context terpisah dari request context.
- `make quality` lulus: formatting, vet, staticcheck, full `go test -race ./...`,
  `sqlc-diff`, migration validation, dan compose validation. Integration race package
  selesai dalam 167.299 detik pada run tersebut.
