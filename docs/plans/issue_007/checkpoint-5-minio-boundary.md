# Checkpoint 5 — Public-host Presign dan MinIO HeadObject

Status: belum dimulai. Saat ini `compose.yaml` baru menyediakan PostgreSQL dan config
belum memiliki S3 fields.

## Tujuan

Mengimplementasikan narrow ObjectStorage port dengan AWS SDK for Go v2 dan
membuktikannya terhadap MinIO nyata: URL ditandatangani memakai public endpoint,
client dapat PUT object, lalu confirm membaca metadata nyata melalui `HeadObject`.

Host pada URL tidak boleh ditulis ulang setelah signing karena host adalah bagian
dari S3 signature.

## Candidate files

- object-storage adapter baru di bawah `internal/adapter/`;
- integration test MinIO di `tests/integration/`;
- `internal/platform/config/config.go` dan tests;
- `.env.example`;
- `compose.yaml`;
- `cmd/api/main.go`;
- fake deterministic extractor di boundary/package yang ditentukan application port.

Nama package dan constructor harus muncul dari test/port yang sudah ada, bukan dari
template generik.

## Bagian user

1. Verifikasi dulu bentuk storage key dan convention deterministic extractor yang
   belum tertulis exact. Jangan mengarang keduanya.
2. Tulis satu integration test real MinIO untuk jalur:
   presign public URL -> HTTP PUT object JPEG non-zero -> `HeadObject` -> metadata
   yang sesuai. Test harus gagal jelas bila Docker unavailable, bukan `t.Skip`.
3. Tambahkan konfigurasi minimum MinIO pada test fixture/compose dan AWS SDK client
   sampai tracer itu GREEN.
4. Implementasikan satu adapter operation `HeadObject` atau presign terlebih dulu
   sesuai test tracer. Map AWS SDK output menjadi application-owned type.
5. Stop untuk review sebelum menambah matrix media/size/error.

## Review agent

Agent memeriksa public vs internal endpoint, path-style setting, bucket isolation,
fixture cleanup, URL tidak bocor ke log, SDK error tidak bocor ke HTTP, context
propagation, metadata content type/size/ETag, dan tidak ada database transaction yang
terbuka selama request MinIO.

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
