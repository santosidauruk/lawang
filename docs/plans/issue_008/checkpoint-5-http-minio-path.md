# Checkpoint 5 — Public HTTP, PostgreSQL, dan Real MinIO Path

Status: approved; menunggu Checkpoint 4 selesai.

## Tujuan

Membuktikan satu tracer publik lengkap:

`upload-url -> returned presigned URL PUT -> confirm -> durable biometric outcome`.

Route, request/response shape, S3 adapter, config, dan runtime wiring direuse dari
Issue 007. Checkpoint ini tidak membuat handler, route, bucket, atau S3 adapter kedua.

## Perilaku tracer

Dengan session yang sudah memiliki accepted Identity Document Verification Artifact:

1. `POST .../artifacts/upload-url` dengan `{"kind":"biometric_capture"}` returns
   `201` exact fields `uploadIntentId` dan `uploadUrl`;
2. client melakukan direct HTTP PUT JPEG non-zero ke returned URL tanpa host rewrite;
3. `POST .../artifacts/confirm` dengan returned intent returns `200` summary exact
   state `biometric_capture_uploaded`;
4. PostgreSQL menyimpan satu accepted biometric Verification Artifact dan satu
   `confirm_biometric_capture` event;
5. readiness predicate menjadi true;
6. DocumentExtractor tidak dipanggil.

## Candidate files

- `tests/integration/artifact_http_postgres_test.go`
- `tests/integration/artifact_minio_test.go`
- `internal/adapter/httpapi/artifact_upload_http_test.go`
- `internal/adapter/httpapi/artifact_http_test.go`
- HTTP error mapping/router hanya bila RED menunjukkan hard-coded identity contract;
- runtime wiring existing sebagai regression target, bukan tempat membuat service
  biometric terpisah.

## Bagian user

1. `[test fixture][object-storage adapter]` User memakai fixture PostgreSQL + MinIO
   existing, membuat bucket unik, dan mendaftarkan container/resource cleanup tanpa
   `t.Skip` ketika Docker tidak tersedia.
2. `[test fixture][postgres adapter]` User seed Verification Session
   `identity_document_uploaded` beserta accepted Identity Document Verification
   Artifact; state tidak boleh diubah tanpa accepted evidence row yang sesuai.
3. `[test fixture][application service]` User memasang fail-fast DocumentExtractor
   atau call counter yang menuntut zero call pada seluruh biometric tracer.
4. `[integration test][http handler]` User mengirim authenticated
   `POST /verification-sessions/{id}/artifacts/upload-url` dengan exact body
   `{"kind":"biometric_capture"}` melalui handler publik nyata.
5. `[integration test][http handler]` User mengassert response `201` hanya memiliki
   `uploadIntentId` dan `uploadUrl`, lalu memakai kedua nilai response tersebut tanpa
   menebak storage key.
6. `[integration test][object-storage adapter]` User melakukan direct HTTP PUT JPEG
   non-zero ke returned signed URL dengan content type `image/jpeg`, tanpa credentials
   dan tanpa host rewrite.
7. `[integration test][http handler]` User mengirim authenticated confirm request
   melalui handler publik memakai exact Upload Intent ID dari response upload-url.
8. `[integration test][http handler]` User mengassert response `200` memiliki current
   session summary dengan exact state `biometric_capture_uploaded`.
9. `[integration test][postgres adapter]` User mengassert durable PostgreSQL outcome:
   satu confirmed biometric intent, satu accepted biometric Verification Artifact,
   satu `confirm_biometric_capture` event, dan readiness predicate `true`.
10. `[integration test][application service]` User mengassert HeadObject membaca
    metadata nyata dan DocumentExtractor call count tetap nol.
11. `[verification]` User menjalankan tracer dan mencatat RED pertama pada public
    contract atau layer yang belum mendukung biometric. Jika seluruh perubahan
    Checkpoint 1-4 membuat tracer langsung GREEN, jangan membuat code tambahan.
12. `[http handler]` Hanya jika RED menunjukkan contract masih hard-coded ke identity,
    user menulis minimal GREEN untuk exact accepted-kind validation/message tanpa
    membuat route atau handler biometric kedua.
13. `[runtime wiring]` Hanya jika RED menunjukkan dependency existing belum tersambung,
    user memperbaiki wiring minimum tanpa membuat service atau S3 adapter kedua.
14. `[verification]` User menjalankan tracer sampai GREEN dan menjalankan satu full
    Identity Document HTTP + PostgreSQL + MinIO tracer sebagai regression minimum.
15. `[review]` User berhenti dan menyerahkan tracer, perubahan minimum, serta output
    RED/GREEN kepada agent sebelum PNG/PDF/size/error matrix ditambahkan.

User memperbaiki concept-bearing public tracer bila ia melewati route, memakai SDK
PutObject, merewrite signed host, atau seed readiness yang tidak valid.

## Review agent

Agent memeriksa:

- exact auth/method/JSON/response fields tetap kompatibel Issue 007;
- upload-url message untuk bounded kinds mengikuti wording yang sudah di-approve;
- public presign/internal HeadObject client tetap terpisah;
- real HTTP PUT memakai signed URL tanpa credentials dan tanpa host rewrite;
- biometric uses actual `HeadObject` metadata and no extractor;
- test memakai bucket unik, cleanup lifecycle, dan tidak `t.Skip` saat Docker absent;
- error/log tidak membocorkan URL, key, bucket, endpoint, atau credential.

## Bagian agent

Setelah tracer JPEG GREEN, agent menutup sibling cases:

1. PNG real MinIO success;
2. PDF, empty object, dan > 5 MiB rejection melalui metadata nyata;
3. wrong state/kind, expired/superseded intent, missing object, dan unavailable S3
   menjadi bounded HTTP errors;
4. strict JSON/auth/method regressions dan exact success/error response shapes;
5. dalam integration fixture yang sama, readiness predicate false sebelum biometric
   confirm dan true sesudah durable artifact, tanpa menambah public readiness field;
6. Identity Document HTTP + PostgreSQL + MinIO regression tetap GREEN.

## Definition of done

- user-authored end-to-end tracer direview dan GREEN;
- JPEG/PNG success serta PDF/empty/>5 MiB failures terbukti terhadap MinIO nyata;
- no extractor call terbukti;
- exact HTTP compatibility dan safe errors terbukti;
- readiness berubah hanya karena accepted artifact commit;
- runtime tidak memiliki duplicate biometric route/service/storage adapter.

Final verification harus mencatat test names aktual, Docker status, focused MinIO
suite, dan race-safe package tests. Jangan memakai developer database atau silent
skip.
