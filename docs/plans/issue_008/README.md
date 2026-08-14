# Issue 008 Collaboration Plan

Status: approved on 2026-08-12; Checkpoint 1-2 selesai, Checkpoint 3
`ready-for-human`.

Dokumen ini adalah pintu masuk untuk Issue 008. Ia membagi kontrak Biometric Capture
menjadi checkpoint belajar yang tetap menghasilkan perilaku observable, bukan fase
horizontal "semua test lalu semua implementation".

Urutan sumber kebenaran:

1. [`docs/plan-go.md`](../../plan-go.md), terutama bagian 4-5.3, 8-9, 14, 16 Issue 8,
   dan 17.2;
2. [`docs/issues/008-confirm-biometric-capture-uploads.md`](../../issues/008-confirm-biometric-capture-uploads.md);
3. [`CONTEXT.md`](../../../CONTEXT.md);
4. kontrak Issue 007 yang sudah GREEN di
   [`docs/issues/007-confirm-identity-document-uploads.md`](../../issues/007-confirm-identity-document-uploads.md);
5. [`docs/learning/007-upload-intents-and-identity-validation.md`](../../learning/007-upload-intents-and-identity-validation.md);
6. plan checkpoint dalam direktori ini.

Jika sumber tersebut bertentangan, jangan memilih diam-diam. Tunjukkan
pertentangannya dan hentikan bagian yang bergantung pada keputusan itu.

## Hasil audit checkout sebelum penulisan plan

- Issue 007 dan Checkpoint 6 sudah GREEN; replay, concurrent confirm, session-level
  advisory lock, PostgreSQL, HTTP, dan real MinIO tidak perlu dibangun ulang.
- schema `upload_intents` dan `verification_artifacts` sudah menerima bounded kind
  `biometric_capture`, termasuk ownership FK dan unique artifact per `(session, kind)`.
- state/event domain sudah memiliki transisi
  `identity_document_uploaded` + `confirm_biometric_capture` ->
  `biometric_capture_uploaded`.
- `UploadIntentService.Create` dan `artifact.Service.Confirm` masih membekukan jalur
  `identity_document`; di sinilah RED pertama Issue 008 seharusnya muncul.
- route upload-url dan confirm sudah kind-neutral pada bentuk URL/request. Pesan error
  upload-url masih mengatakan hanya `identity_document` yang didukung.
- belum ada query readiness yang membuktikan dua accepted Verification Artifact untuk
  Verification Session yang sama.
- focused baseline sebelum perubahan dokumen lulus:
  `go test ./internal/application/artifact ./internal/adapter/httpapi -count=1`.

## Aturan pembagian user dan agent

Setiap checkpoint mengikuti aturan yang telah digunakan di Issue 007:

1. agent menjelaskan satu konsep dan menyiapkan deliverable terkecil;
2. **bagian user** adalah tracer pertama atau operasi pertama yang mengandung konsep
   baru pada checkpoint tersebut;
3. user menjalankan satu proof/test sampai RED, lalu menulis minimal GREEN;
4. agent mereview correctness, transaction, concurrency, domain language, dan batas
   adapter; kesalahan concept-bearing dikembalikan kepada user untuk direvisi;
5. **bagian agent** dimulai hanya setelah bagian user benar dan GREEN;
6. agent mengambil sibling/repetitive cases, regression, safe error mapping, dan
   verification gate, satu RED -> GREEN per perilaku;
7. refactor hanya dilakukan saat GREEN dan hanya bila invariant bersama sudah nyata;
8. learning note diperbarui pada penutupan checkpoint, bukan sebelum buktinya ada.

User tetap memegang irisan pertama di application, PostgreSQL, readiness, public
HTTP/MinIO, dan concurrency. Agent tidak boleh mengganti bagian fundamental itu
diam-diam hanya agar checkpoint cepat selesai.

## Vocabulary tag `Bagian user`

Setiap poin di `Bagian user` diawali maksimal dua tag. Tag pertama menunjukkan jenis
pekerjaan atau bukti. Tag kedua, bila ada, menunjukkan layer yang disentuh.

Jenis pekerjaan atau bukti:

- `[unit test]`: test terfokus dengan fake atau memory implementation tanpa
  PostgreSQL/MinIO nyata;
- `[integration test]`: test yang melewati PostgreSQL, MinIO, HTTP route nyata, atau
  beberapa boundary sekaligus;
- `[sql proof]`: pembuktian constraint atau perilaku SQL secara langsung;
- `[test fixture]`: setup data, fake, clock, container, atau dependency test;
- `[verification]`: menjalankan dan mencatat RED, GREEN, race detector, atau quality
  gate;
- `[review]`: titik berhenti untuk menyerahkan hasil kepada agent sebelum pekerjaan
  sibling dimulai.

Layer pekerjaan:

- `[domain]`: kind, state transition, event type, atau invariant murni;
- `[application service]`: orchestration use case seperti `Create` atau `Confirm`;
- `[http handler]`: decode request, auth, response, dan HTTP error mapping;
- `[sql query]`: SQL produksi yang ditulis manual di `sql/queries/` dan menjadi input
  `sqlc generate`;
- `[postgres adapter]`: transaction operation serta mapping antara generated SQLC
  type dan application-owned type;
- `[object-storage adapter]`: presign dan `HeadObject` melalui S3/MinIO;
- `[runtime wiring]`: config, constructor, dan penyambungan dependency di entrypoint;
- `[migration]`: perubahan schema di `sql/migrations/`, hanya bila RED atau proof
  menunjukkan schema existing tidak cukup.

SQL seed/assertion yang hanya hidup di integration test memakai `[test fixture]` atau
`[integration test]`, bukan `[sql query]`. Generated files di
`internal/adapter/postgres/sqlc/` tidak diedit manual. Satu poin memiliki satu tindakan
utama; penulisan test, pembuktian RED, minimal GREEN, pembuktian GREEN, dan review
harus menjadi poin terpisah.

## Enam checkpoint yang disetujui

| # | Checkpoint | Observable outcome | Panduan |
| --- | --- | --- | --- |
| 1 | Kind policy dan Biometric Upload Intent | `biometric_capture` menghasilkan intent/key/URL dengan guard urutan yang benar | [checkpoint-1](checkpoint-1-biometric-upload-intent.md) |
| 2 | Application confirmation tanpa extractor | metadata JPEG/PNG <= 5 MiB menghasilkan accepted biometric outcome | [checkpoint-2](checkpoint-2-application-confirmation.md) |
| 3 | PostgreSQL atomic outcome | intent, Verification Artifact, state, dan event commit/rollback sebagai satu unit | [checkpoint-3](checkpoint-3-postgresql-outcome.md) |
| 4 | Artifact-derived readiness predicate | readiness benar hanya karena dua accepted artifact milik session yang sama | [checkpoint-4](checkpoint-4-submission-readiness.md) |
| 5 | Public HTTP -> PostgreSQL -> MinIO | client presign/PUT/confirm biometric melalui route nyata | [checkpoint-5](checkpoint-5-http-minio-path.md) |
| 6 | Replay, concurrency, dan regression | concurrent/replayed confirm tidak mengulang external work atau database effects | [checkpoint-6](checkpoint-6-replay-concurrency-regression.md) |

Checkpoint 5 sengaja menggabungkan HTTP dan MinIO. Kedua boundary sudah dibuktikan
terpisah oleh Issue 007; nilai belajar baru di Issue 008 adalah membuktikan kind
biometric melewati jalur publik lengkap tanpa extractor, bukan menulis handler atau
adapter S3 kedua.

## Scope antark-checkpoint

- Checkpoint 1 tidak mengonfirmasi object.
- Checkpoint 2 memakai memory/fake boundary dan tidak membuktikan PostgreSQL/MinIO.
- Checkpoint 3 memakai PostgreSQL nyata tetapi ObjectStorage masih fake.
- Checkpoint 4 membuktikan predicate data; ia tidak membuat submit route atau outbox.
- Checkpoint 5 memakai route, PostgreSQL, presigned HTTP PUT, dan MinIO nyata tetapi
  tidak menutup seluruh race matrix.
- Checkpoint 6 menutup replay/concurrency dan regression Identity Document.
- Provider submission, `verification_pending`, outbox, Redis/Asynq, dan provider
  worker tetap Issue 009.

## Keputusan yang sudah dibekukan oleh source of truth

- kind exact adalah `biometric_capture`.
- hanya JPEG/PNG, non-zero, maksimum 5 MiB; PDF harus ditolak.
- Biometric Capture tidak menjalankan `DocumentExtractor` dan tidak menghasilkan
  Local Validation Failure dokumen.
- urutan publik tetap Identity Document lalu Biometric Capture.
- storage key harus memisahkan kind. Bentuk yang konsisten dengan invariant Issue 007
  adalah `verification-sessions/{sessionID}/biometric_capture/{intentID}`.
- TTL URL dan Upload Intent tetap lima menit.
- external storage I/O tidak berjalan di dalam database transaction.
- success mengonfirmasi intent, membuat tepat satu accepted Biometric Verification
  Artifact, berpindah ke `biometric_capture_uploaded`, dan menulis satu
  `confirm_biometric_capture` event secara atomik.
- readiness berasal dari accepted Verification Artifact milik session yang sama,
  bukan stored object, Upload Intent, status intent, atau session state saja.
- route, auth, upload-url response, confirm request, dan success response shape tetap
  milik Issue 007.

## Keputusan yang disetujui user pada 2026-08-12

1. **Readiness boundary.** Issue 008 menambahkan internal transaction-compatible
   predicate (pemeriksaan kondisi ya/tidak yang dapat dijalankan memakai transaction
   database aktif) dan integration proof. Tidak ada public readiness endpoint atau
   field `ready`. Standalone `ready=true` bukan authorization untuk submit dan dapat
   menjadi stale sebelum transaction submission dimulai.
2. **Invalid-kind message.** Pesan upload-url menjadi
   `only identity_document and biometric_capture uploads are supported`, tetap
   `400 INVALID_UPLOAD_INTENT_KIND`.
3. **Application abstraction.** Behavior/policy kind tetap explicit, tetapi nama dan
   bentuk interface/helper tidak dibekukan sebelum tracer Checkpoint 1 dan 2 RED.
   Satu service bersama boleh dipertahankan bila test membuktikan invariant bersama;
   jangan membuat generic artifact framework atau handler biometric kedua.
4. Setiap poin `Bagian user` memakai vocabulary tag di atas dan dipecah menjadi satu
   tindakan utama per poin.

## Handoff checkpoint aktif

Checkpoint 2 selesai pada 2026-08-13. Mulai hanya dari Checkpoint 3:

1. baca panduan Checkpoint 3 dan PostgreSQL integration fixture aktual;
2. jalankan `git status --short --untracked-files=all`, cek Docker Desktop, dan
   focused PostgreSQL baseline;
3. agent sudah menyiapkan
   `TestBiometricConfirmPersistsPostgresOutcomeAtomically` tanpa mengisi fixture,
   call, atau assertion tracer user;
4. poin user 1-8 sudah diimplementasikan sebagai satu real PostgreSQL Biometric
   Confirm success tracer;
5. focused tracer poin 9 direct GREEN pada 2026-08-14;
6. poin 10-11 tidak diperlukan dan poin 12 selesai tanpa SQLC diff karena tidak ada
   SQL, adapter, atau generated file yang berubah;
7. lakukan poin 13 `[review]` sebelum rollback/constraint sibling work.

Jangan men-scaffold Checkpoint 4-6 sebelum Checkpoint 3 mencapai review gate.
