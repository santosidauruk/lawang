# Checkpoint 6 — Replay dan Concurrent Confirm

Status: belum dimulai. Ini hardening terakhir setelah PostgreSQL, HTTP, dan MinIO path
nyata bekerja.

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

## Bagian user

1. Konfirmasi key lock sebelum code. Kandidat paling sempit adalah Upload Intent ID,
   agar confirm intent berbeda tidak saling menghambat, tetapi jangan implementasikan
   kandidat ini tanpa keputusan eksplisit.
2. Tulis satu real PostgreSQL concurrency test yang menjalankan dua public Confirm
   calls untuk intent sama dengan start signal yang sama dan boundary fake yang aman
   untuk concurrency serta menghitung external calls.
3. Assertion awal: kedua caller mendapat outcome sukses yang sama; hanya satu artifact
   dan event tersimpan; `HeadObject` dan extractor masing-masing dipanggil sekali.
4. Jalankan hingga RED terhadap implementation tanpa coordination. Jangan memakai
   sleep untuk menentukan winner.
5. Implementasikan acquire/unlock advisory lock minimum pada dedicated connection,
   termasuk `defer` cleanup pada error/cancellation, sampai tracer GREEN.
6. Stop untuk review.

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
