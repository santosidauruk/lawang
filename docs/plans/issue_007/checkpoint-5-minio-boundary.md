# Checkpoint 5 — Public-host Presign dan MinIO HeadObject

Status: selesai pada 2026-08-03. Public-host presign, direct HTTP PUT, real MinIO
`HeadObject`, bounded storage failures, runtime readiness, dan tracer lengkap
HTTP -> PostgreSQL -> MinIO -> confirm telah terbukti. Replay/concurrency tetap
milik Checkpoint 6.

## Tujuan

Mengimplementasikan narrow ObjectStorage port dengan AWS SDK for Go v2 dan
membuktikannya terhadap MinIO nyata: URL ditandatangani memakai public endpoint,
client dapat PUT object, lalu confirm membaca metadata nyata melalui `HeadObject`.

Host pada URL tidak boleh ditulis ulang setelah signing karena host adalah bagian
dari S3 signature.

## Candidate files

- object-storage adapter baru di bawah `internal/adapter/`, dengan nama package dan
  constructor yang dipilih setelah tracer pertama membutuhkannya;
- `tests/integration/artifact_minio_test.go`;
- `internal/platform/config/config.go` dan tests;
- `.env.example`;
- `compose.yaml`;
- `cmd/api/main.go`;
- fake deterministic extractor di boundary/package yang ditentukan application port.

Nama package dan constructor harus muncul dari test/port yang sudah ada, bukan dari
template generik.

## Audit kontrak sebelum mulai

Dua hal yang disebut belum exact pada versi awal checkpoint ini ternyata sudah
dibekukan oleh source of truth dan code saat ini:

1. Identity Document storage key adalah
   `verification-sessions/{sessionID}/identity_document/{intentID}`. Bentuk ini
   dibuat oleh `artifact.UploadIntentService` dan sudah dibuktikan oleh application,
   PostgreSQL, serta HTTP + PostgreSQL tests.
2. Fake deterministic extractor dikontrol melalui explicit result/error map
   berdasarkan storage key. Missing key adalah explicit error; fake tidak menebak
   identity number dari filename, metadata object, atau body.

Jangan membuka ulang dua keputusan itu di Checkpoint 5. Keputusan advisory-lock key
tetap milik Checkpoint 6.

Application-owned boundary yang harus dipenuhi adapter sudah tersedia:

```go
type UploadPresigner interface {
    PresignUpload(context.Context, string, time.Duration) (string, error)
}

type ObjectStorage interface {
    HeadObject(context.Context, string) (ObjectMetadata, error)
}
```

Satu concrete adapter boleh memenuhi kedua port tersebut. AWS SDK types hanya hidup
di dalam adapter; `HeadObject` harus memetakan output SDK ke
`artifact.ObjectMetadata`.

## Topologi client yang akan dibuktikan

Gunakan satu adapter boundary dengan dua konfigurasi S3 client:

- internal client memakai `S3_INTERNAL_ENDPOINT` untuk bucket readiness dan
  `HeadObject`;
- presign client dibuat dari S3 client yang memakai `S3_PUBLIC_ENDPOINT`;
- keduanya memakai region, credentials, bucket, dan path-style setting yang sama.

Jangan menandatangani internal URL lalu mengganti host. Untuk tracer Testcontainers,
mapped endpoint yang dapat dijangkau proses test menjadi public endpoint dan host
returned URL harus sama dengannya. Perbedaan
`http://minio:9000` versus `http://localhost:9000` dibuktikan saat Compose/runtime
wiring, bukan dengan string replacement.

Gunakan AWS SDK v2 juga untuk membuat bucket fixture; jangan menambah MinIO-specific
SDK hanya untuk setup. Setiap test memakai bucket unik dan mendaftarkan container
cleanup melalui test lifecycle.

## Bagian user

1. Ubah scaffold `checkpoint5MinIOPresignPutHeadObject` di
   `tests/integration/artifact_minio_test.go` menjadi test bernama
   `TestMinIOPresignPutAndHeadObjectReturnsActualMetadata`.
2. Tulis satu integration test real MinIO untuk satu jalur saja:
   presign public URL -> HTTP PUT object JPEG non-zero -> `HeadObject` -> metadata
   yang sesuai. Test harus gagal jelas bila Docker unavailable, bukan `t.Skip`.
3. Gunakan fixed session/intent UUID untuk membentuk storage key yang telah
   dibekukan, tetapi gunakan bucket unik agar test terisolasi.
4. Tambahkan dependency minimum AWS SDK v2 dan Testcontainers MinIO module, lalu
   konfigurasi fixture dan AWS SDK client sampai tracer itu GREEN. Jangan mengubah
   `compose.yaml`, runtime config, atau `cmd/api` dalam siklus tracer pertama.
5. Kirim HTTP PUT langsung ke URL yang dikembalikan presigner, set
   `Content-Type: image/jpeg`, gunakan bytes JPEG non-zero, dan perlakukan non-2xx
   sebagai failure yang menyertakan status serta body terbatas.
6. Implementasikan satu adapter operation `HeadObject` atau `PresignUpload` terlebih
   dulu sesuai RED tracer. Map AWS SDK output menjadi application-owned type.
7. Assert URL host terhadap configured public endpoint sebelum PUT. Assert actual
   `ContentType`, `SizeBytes`, dan non-empty `ETag` sesudah `HeadObject`.
8. Stop untuk review sebelum menambah operation kedua atau matrix media/size/error.

Scaffold sengaja bukan fungsi `Test...` dan tidak memakai `t.Skip`, sehingga suite
existing tetap GREEN tetapi belum ada klaim test MinIO. Renaming menjadi `Test...`
harus menghasilkan RED pertama sampai fixture/tracer benar-benar ditulis.

## Urutan RED -> GREEN pertama

Jangan menulis seluruh adapter dan matrix sekaligus:

1. RED karena fixture belum dapat membuat MinIO dan bucket nyata.
2. GREEN fixture saja: container hidup, endpoint didapat, bucket unik dibuat.
3. RED pada `PresignUpload`.
4. GREEN presign minimum dengan exact expiry argument dari port.
5. RED pada HTTP PUT atau signature/public host.
6. GREEN direct PUT tanpa host rewrite.
7. RED pada `HeadObject`.
8. GREEN mapping metadata ke `artifact.ObjectMetadata`.

Pada tahap 8 hanya tracer JPEG yang perlu GREEN. Baru setelah review, agent
mengerjakan sibling behavior satu per satu.

## Review agent

Agent memeriksa public vs internal endpoint, path-style setting, bucket isolation,
fixture cleanup, URL tidak bocor ke log, SDK error tidak bocor ke HTTP, context
propagation, metadata content type/size/ETag, dan tidak ada database transaction yang
terbuka selama request MinIO.

Review juga memeriksa bahwa:

- test benar-benar melakukan HTTP PUT, bukan memanggil SDK `PutObject`;
- public host berasal dari config client yang digunakan saat signing;
- request PUT tidak mengirim credentials;
- `HeadObject` memakai internal client dan bucket configured, bukan bucket/key yang
  dikodekan di application;
- ETag diperlakukan sebagai opaque metadata, bukan checksum yang dihitung ulang;
- Docker unavailable menjadi `t.Fatalf` dari startup fixture, bukan skip.

## Kelanjutan agent

Setelah real upload tracer GREEN:

1. lengkapi presign operation dan five-minute expiry test;
2. prove public-host URL dapat langsung digunakan tanpa host rewrite;
3. prove JPEG, PNG, PDF success melalui metadata nyata;
4. prove zero-byte, over-10-MiB, dan unsupported content type rejection;
5. prove missing object dan transient SDK failures menjadi bounded application/HTTP
   failures tanpa credential, bucket detail, atau presigned URL leakage;
6. implement deterministic fake extractor dan prove match serta
   `identity_number_mismatch` menggunakan convention yang telah disetujui;
7. wire config/adapter ke runtime dan tambahkan readiness failure yang jelas;
8. jalankan route nyata dengan PostgreSQL + MinIO untuk upload-url, PUT, dan confirm.

## Definition of done

- real MinIO presign/PUT/HeadObject lulus;
- returned URL menggunakan configured public host dan signature valid;
- application tidak menerima AWS SDK types;
- media/size/empty constraints berasal dari actual HeadObject metadata;
- safe SDK error mapping terbukti;
- deterministic extraction match/mismatch repeatable tanpa raw extraction storage;
- compose/config tests dan existing tests tetap GREEN.

Command final harus menunjuk nama real test yang dibuat. Jangan menulis command
hipotetis sebagai bukti selesai. Selain focused MinIO tests, jalankan race detector
untuk package yang tidak memerlukan shared unsafe fixture.

## Completion evidence

- Docker-unavailable path gagal melalui `t.Fatalf`, tanpa panic atau skip.
- JPEG, PNG, dan PDF memakai public-host presigned URL yang dikirim langsung melalui
  `net/http`, lalu metadata nyata dibaca melalui internal-client `HeadObject`.
- Empty, lebih dari 10 MiB, dan unsupported content type ditolak berdasarkan metadata
  MinIO nyata.
- Missing object dan endpoint S3 yang tidak tersedia menjadi bounded
  `503 OBJECT_STORAGE_FAILED` tanpa storage key, endpoint, atau bucket leakage.
- Concrete deterministic extractor membuktikan configured success/error, missing key,
  cancellation, dan PostgreSQL mismatch atomik.
- Runtime memakai public client untuk presign, internal client untuk readiness serta
  `HeadObject`, dan berhenti bila bucket tidak siap.
- `TestIdentityDocumentUploadPutAndConfirmOverHTTPWithPostgreSQLAndMinIO` membuktikan
  upload-url -> returned URL PUT -> confirm -> satu artifact/event yang durable.
