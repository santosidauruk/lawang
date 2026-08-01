# Issue 007 Collaboration Plan

Dokumen ini adalah pintu masuk untuk melanjutkan Issue 007 dari awal percakapan,
termasuk bila agent yang memandu tidak memiliki konteks thread sebelumnya. Dokumen
ini tidak menggantikan kontrak produk. Urutan sumber kebenaran adalah:

1. [`docs/plan-go.md`](../../plan-go.md), terutama bagian 4-5.3, 6.4, 7.1, 8-9,
   12, 14, 16 Issue 7, dan 17.2;
2. [`docs/issues/007-confirm-identity-document-uploads.md`](../../issues/007-confirm-identity-document-uploads.md);
3. [`CONTEXT.md`](../../../CONTEXT.md);
4. [`docs/learning/007-upload-intents-and-identity-validation.md`](../../learning/007-upload-intents-and-identity-validation.md);
5. plan dan panduan checkpoint dalam direktori ini.

Jika sumber-sumber tersebut bertentangan, jangan memilih diam-diam. Tunjukkan
pertentangannya kepada user dan hentikan bagian yang bergantung pada keputusan itu.

## Cara kolaborasi

Setiap checkpoint menggunakan pola yang sama:

1. agent menjelaskan satu konsep dan menyiapkan deliverable terkecil;
2. user menulis bagian pertama yang paling banyak mengandung pembelajaran;
3. user menjalankan satu test/proof hingga RED atau GREEN sesuai instruksi;
4. agent mereview correctness, transaksi, concurrency, dan batas antarlayer;
5. user memperbaiki kesalahan yang concept-bearing; agent tidak menggantinya diam-diam;
6. setelah konsep terbukti, agent menulis sibling/repetitive work satu siklus
   RED -> GREEN pada satu waktu;
7. agent menjalankan verifikasi checkpoint dan memperbarui learning note.

Jangan menulis semua test terlebih dahulu lalu seluruh implementasi. Satu perilaku
publik harus menjadi GREEN sebelum berpindah ke perilaku berikutnya.

## Enam checkpoint

| # | Checkpoint | Status saat dokumen dibuat | Panduan |
| --- | --- | --- | --- |
| 1 | Schema dan invariant Upload Intent | selesai dan telah direview | [checkpoint-1](checkpoint-1-schema-and-invariants.md) |
| 2 | Konfirmasi sukses dan outcome aplikasi | selesai; focused suite dan race detector GREEN | [checkpoint-2](checkpoint-2-application-confirmation.md) |
| 3 | PostgreSQL transaction dan adapter | selesai; schema, confirm, rollback, create/supersede, dan concurrency GREEN | [checkpoint-3](checkpoint-3-postgresql-boundary.md) |
| 4 | Strict HTTP path dan runtime wiring | selesai; strict contracts, bounded errors, dan HTTP + PostgreSQL tracer GREEN | [checkpoint-4](checkpoint-4-http-path.md) |
| 5 | Public-host presign dan MinIO `HeadObject` | aktif; scaffold dan executable handoff siap | [checkpoint-5](checkpoint-5-minio-boundary.md) |
| 6 | Replay dan concurrent-confirm coordination | belum dimulai | [checkpoint-6](checkpoint-6-replay-and-concurrency.md) |

Urutan ini adalah urutan belajar, bukan pemisahan horizontal layer. Pada setiap
checkpoint tetap buat satu jalur publik yang dapat diamati end-to-end dalam cakupan
checkpoint tersebut.

## Keputusan yang sudah dibekukan

- Upload Intent, stored object, dan Verification Artifact adalah tiga hal berbeda.
- `identity_document` menerima JPEG, PNG, atau PDF, harus non-zero, maksimum 10 MiB.
- TTL URL dan Upload Intent adalah lima menit.
- Response upload URL tidak menambahkan `expiresAt`.
- Presign dan semua object-storage/extractor I/O berjalan tanpa transaction database
  yang terbuka.
- Setelah external I/O, transaction singkat mengunci dan membaca ulang semua guard.
- Tidak ada status `confirming`.
- Accepted Verification Artifact tidak dihapus otomatis.
- Mismatch minimum adalah `identity_number_mismatch`; ia menyimpan outcome terbatas,
  tidak menyimpan raw extraction.
- Checkpoint 2 membekukan error code `UPLOAD_INTENT_NOT_FOUND`,
  `UPLOAD_INTENT_EXPIRED`, `UPLOAD_INTENT_SUPERSEDED`,
  `INVALID_UPLOAD_INTENT_KIND`, `INVALID_OBJECT_METADATA`,
  `OBJECT_STORAGE_FAILED`, `DOCUMENT_EXTRACTION_FAILED`, dan `CONFIRMATION_STALE`.
- Replay `confirmed` dan `validation_failed` tidak mengulang external I/O atau event.
- Identity Document storage key menggunakan
  `verification-sessions/{sessionID}/identity_document/{intentID}`.
- Upload-url success adalah `201 Created` dengan exact fields `uploadIntentId` dan
  `uploadUrl`, tanpa `expiresAt` atau `Location`.
- Checkpoint 4 HTTP validation dan bounded artifact error mapping telah dibekukan di
  `checkpoint-4-http-path.md`, termasuk mismatch `422`, object metadata `422`,
  stale/superseded/expired `409`, storage `503`, extraction `500`, dan safe unknown
  `500`.
- Fake deterministic extractor untuk Checkpoint 4 dikontrol melalui explicit result
  map berdasarkan storage key. Ia tidak membaca identity number dari filename atau
  object metadata.
- `cmd/api` sengaja tidak mengaktifkan artifact routes sampai real MinIO boundary
  tersedia pada Checkpoint 5; Checkpoint 4 membuktikan handler dan HTTP + PostgreSQL
  path dengan explicit fakes.
- Strategi koordinasi yang dipilih untuk checkpoint 6 adalah PostgreSQL
  session-level advisory lock; kata *session* di sini berarti sesi koneksi PostgreSQL,
  bukan Verification Session.

## Keputusan yang masih harus diverifikasi, bukan diasumsikan

Sebelum interface publik terkait ditulis, agent harus mencari keputusan ini dalam
thread aktif atau meminta user menjawabnya:

- key advisory lock: kandidat paling sempit adalah Upload Intent ID, tetapi pilihan
  ini harus dikonfirmasi sebelum SQL/Go lock dibuat.

Kontrak mismatch sudah exact: HTTP `422`, code `LOCAL_VALIDATION_FAILED`, message dan
`details.reason=identity_number_mismatch` mengikuti issue document.

## Handoff saat ini

Checkpoint 1 dan Checkpoint 2 sudah selesai. Test user-authored serta sibling behavior
berada di `internal/application/artifact/service_test.go`, dengan implementasi di
`internal/application/artifact/service.go`. Focused suite dan race detector lulus saat
cache build diarahkan ke lokasi writable:

```sh
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -count=1
GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
```

Checkpoint 3 dan Checkpoint 4 selesai. Strict confirm/upload-url contracts, bounded
error mapping, serta public HTTP upload-url -> confirm tracer dengan PostgreSQL
disposable telah GREEN. `cmd/api` sengaja tetap mengirim dependency artifact `nil`;
usable public-host presign dan real MinIO `HeadObject` belum terbukti. Checkpoint 5
adalah checkpoint aktif berikutnya. Replay serta concurrent confirm tetap berada di
Checkpoint 6.

## Yang harus dilakukan agent ketika diminta melanjutkan

1. Baca penuh TDD skill yang tersedia, lalu empat sumber kebenaran di atas.
2. Jalankan `git status --short --untracked-files=all`; semua perubahan yang ada
   adalah milik user sampai terbukti sebaliknya.
3. Buka panduan checkpoint aktif dan file source/test aktual; jangan mengandalkan
   status dokumen jika kode sudah berubah.
4. Jalankan command test/proof terfokus dan laporkan output aktual. Bedakan kegagalan
   kode dari Docker, network, cache, atau sandbox.
5. Tanyakan hanya keputusan yang belum tertulis dan benar-benar mengubah kontrak.
6. Scaffold dengan komentar dan test kecil. Jangan mengisi bagian user sebelum review.
7. Setelah user menyerahkan hasil, review findings berdasarkan severity dan berikan
   file, baris, alasan, serta langkah revisi.
8. Sesudah bagian fundamental benar, implementasikan kelanjutan yang berlabel
   **agent** dalam panduan, satu RED -> GREEN per perilaku.
9. Akhiri checkpoint dengan focused tests, quality gate yang relevan, integration
   proof, dan update learning note. Sebuah checkpoint boleh ditutup hanya sesuai
   Definition of Done panduannya dan harus mencatat dependency yang sengaja ditunda;
   jangan menandai keseluruhan Issue 007 selesai sebelum PostgreSQL dan MinIO nyata
   telah diuji pada checkpoint yang mensyaratkannya.

Informasi minimum yang diperlukan agent untuk memandu adalah: checkpoint aktif,
file yang terakhir diubah user, output command terakhir, apakah Docker tersedia, dan
keputusan interface yang sudah disetujui. Bila salah satunya dapat diperiksa dari
workspace, periksa sendiri; jangan meminta user mengulang informasi yang sudah ada.
