# Checkpoint 6 — Replay dan Concurrent Confirm

Status: workbench user sudah disiapkan; implementation belum dimulai. Ini hardening
terakhir setelah PostgreSQL, HTTP, dan MinIO path nyata bekerja.

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

## Audit checkout sebelum RED

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
5. Real MinIO presign -> HTTP PUT -> `HeadObject` tracer dan sibling JPEG/PNG/PDF
   code sudah ada. Checkpoint 5 belum boleh dianggap selesai hanya dari keberadaan
   code; Definition of Done dan Docker-backed output aktual tetap harus ditutup.

Implikasi RED: implementation sekarang dapat menghasilkan satu sukses dan satu
`CONFIRMATION_STALE`; bila kedua caller overlap sebelum transaction, external counter
juga dapat menjadi dua. Jangan mengunci test pada bentuk RED tertentu karena scheduler
boleh menghasilkan interleaving berbeda. Yang harus RED adalah kontrak publik akhir:
dua recorded success yang sama dan external work tepat sekali.

## Decision gate sebelum lock code

Belum ada lock key yang dibekukan. Rekomendasi paling sempit adalah Upload Intent ID:

- dua confirm untuk intent yang sama wajib serialize;
- intent berbeda tidak saling menunggu;
- UUID harus dipetakan secara deterministic ke bentuk advisory-lock PostgreSQL;
- mapping harus menjelaskan collision risk karena advisory lock tidak menerima UUID
  secara langsung.

User harus menulis keputusan eksplisit di checkpoint ini sebelum menambah SQL atau Go
untuk acquire/unlock. Scaffold test boleh diaktifkan lebih dahulu karena behavior yang
diuji tidak bergantung pada representasi key.

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

Nama interface/constructor belum dibekukan; biarkan compile failure tracer menunjukkan
seam terkecil. Preflight session lookup dan resume-token authorization harus tetap
terjadi sebelum request boleh menunggu advisory lock. Setelah lock diperoleh,
authorization, expiry, intent outcome, dan guard lain tetap dibaca ulang; preflight
tidak boleh dipercaya sebagai commit authority.

Untuk cleanup, `defer conn.Release()` saja belum cukup bila unlock gagal: session-level
lock hidup bersama connection. Adapter harus memastikan connection yang mungkin masih
memegang lock tidak kembali sehat ke pool. Detail ini direview setelah tracer pertama
GREEN, bersama cancellation dan pool-leak tests.

## Workbench

File user disiapkan di
`tests/integration/artifact_confirm_concurrency_test.go`. Fungsinya sengaja bernama
`checkpoint6ConcurrentConfirmReturnsRecordedSuccessOnce`, bukan `Test...`, dan berisi
fatal marker. Karena itu existing suite tetap GREEN dan belum ada klaim concurrency
proof.

## Bagian user

1. **Bekukan key lock.** Tulis keputusan eksplisit apakah Upload Intent ID dipakai.
   Jelaskan isolation scope dan mapping UUID -> advisory key; jangan mulai adapter
   sebelum ini diputuskan.
2. **Aktifkan hanya satu tracer.** Rename
   `checkpoint6ConcurrentConfirmReturnsRecordedSuccessOnce` menjadi
   `TestConcurrentPostgresConfirmReturnsRecordedSuccessAndRunsExternalWorkOnce`, lalu
   hapus fatal marker. Jangan menambah replay/mismatch/cancellation tests dulu.
3. **Arrange PostgreSQL nyata.** Pakai disposable PostgreSQL dan migration artifact,
   seed satu session `personal_details_submitted`, immutable Personal Details, dan satu
   pending `identity_document` intent. Service harus memakai pool, bukan satu
   `*pgx.Conn` yang dipakai bersamaan oleh dua goroutine.
4. **Buat boundary fake concurrency-safe.** `HeadObject` mengembalikan JPEG non-zero
   dengan ETag; extractor mengembalikan identity number yang sama. Counter harus
   dilindungi mutex/atomic dan dibaca setelah kedua caller selesai. Fake tidak boleh
   memalsukan database outcome atau advisory lock.
5. **Mulai dua public calls bersama.** Siapkan dua goroutine sampai keduanya ready,
   lalu `close(start)` sekali. Masing-masing memanggil public `service.Confirm` untuk
   session/intent/token yang sama dan mengirim tepat satu result ke buffered channel.
   Gunakan `WaitGroup`; jangan memakai `time.Sleep` untuk memilih winner.
6. **Assert caller outcome.** Kedua error harus nil. Kedua `session.Summary` harus
   identik: ID sama, status `identity_document_uploaded`, expiry sama. Caller kedua
   menerima recorded success, bukan `CONFIRMATION_STALE`.
7. **Assert durable outcome dari PostgreSQL.** Query committed state dan buktikan
   Upload Intent `confirmed`, tepat satu Verification Artifact untuk intent itu,
   tepat satu `confirm_identity_document` event, dan session berada di
   `identity_document_uploaded`. Jangan hanya mengandalkan unique-violation error.
8. **Assert external behavior.** Sesudah goroutine selesai, `HeadObject` count = 1 dan
   extractor count = 1. Counter adalah observasi boundary publik “external work tidak
   diulang”, bukan assertion terhadap private helper.
9. **Tangkap RED yang bermakna.** Jalankan test terfokus terhadap implementation
   sekarang. Assertion final harus gagal tanpa panic/data race/deadlock. Catat output;
   jangan mengubah expected result menjadi satu sukses/satu stale hanya agar test hijau.
10. **Tambahkan coordinator minimum.** Refactor application seam secukupnya agar
    PostgreSQL adapter meminjam dedicated connection, acquire lock, re-read recorded
    outcome, menjalankan external work tanpa open transaction, lalu memakai transaction
    singkat pada connection yang sama.
11. **Implement replay success minimum.** Di bawah lock, status `confirmed` harus
    membentuk summary tersimpan tanpa `HeadObject`, extraction, write, atau event baru.
    Jangan mengerjakan validation-failed replay pada siklus ini.
12. **Cleanup semua exit.** Unlock harus didaftarkan segera setelah acquire sukses;
    connection release terjadi setelah unlock. Acquire error/cancellation tidak boleh
    menjalankan unlock palsu. Bila unlock gagal, jangan mengembalikan connection yang
    mungkin masih memegang lock ke pool.
13. **GREEN lalu stop.** Jalankan focused test dan race variant. Setelah keduanya GREEN,
    berhenti untuk review sebelum sibling behavior di bagian agent.

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

Agent memeriksa lock ownership/lifetime, deterministic key mapping, collision risk,
pool connection release, cancellation, unlock on every exit, tidak adanya open
transaction saat external I/O, dan apakah test benar-benar memakai dua concurrent
connections/calls.

## Kelanjutan agent

Setelah concurrent-success tracer GREEN:

1. confirmed replay mengembalikan recorded success tanpa external work/event;
2. validation-failed replay mengembalikan exact bounded failure tanpa external work;
3. concurrent mismatch menghasilkan satu failure event dan zero artifacts;
4. supersede/expiry saat request menunggu lock menghasilkan conflict yang tepat;
5. stale result setelah external I/O dibuang tanpa partial writes;
6. external failure melepaskan lock sehingga retry dapat berjalan;
7. context cancellation ketika menunggu lock tidak membocorkan connection/lock;
8. race tests untuk create intent dan confirm;
9. full PostgreSQL + MinIO path membuktikan external work tidak terulang.

## Definition of done

- confirmed dan validation-failed replay exact serta side-effect free;
- concurrent success/mismatch hanya melakukan satu external validation dan satu
  durable outcome;
- unique constraints tetap menjadi defense terakhir;
- tidak ada transaction terbuka selama MinIO/extractor I/O;
- advisory lock selalu dilepas dan pool connection dikembalikan;
- `go test -race ./...`, PostgreSQL concurrency tests, MinIO integration tests,
  `sqlc` diff, migration validation, dan full quality gate lulus;
- issue acceptance checklist dan learning note diperbarui dengan output aktual.

Agent tidak boleh menyatakan Issue 007 selesai hanya dari fake counter. Bukti akhir
harus mencakup PostgreSQL nyata dan MinIO nyata.
