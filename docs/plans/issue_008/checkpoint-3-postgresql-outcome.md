# Checkpoint 3 — PostgreSQL Atomic Biometric Outcome

Status: approved; menunggu Checkpoint 2 selesai.

## Tujuan

Membuktikan application behavior Checkpoint 2 terhadap PostgreSQL disposable tanpa
membuat parallel biometric persistence stack. Existing schema/query/adapter harus
direuse bila sudah generic secara benar; perubahan hanya ditambahkan bila RED nyata
menunjukkan gap.

ObjectStorage masih fake. Real MinIO berada di Checkpoint 5.

## Perilaku tracer

Satu call `artifact.Service.Confirm` pada session
`identity_document_uploaded` dan pending biometric intent harus atomically:

1. mengubah intent menjadi `confirmed`;
2. mempertahankan accepted Identity Document Verification Artifact;
3. menambah tepat satu accepted biometric Verification Artifact;
4. mengubah session menjadi `biometric_capture_uploaded`;
5. menambah tepat satu `confirm_biometric_capture` event.

## Candidate files

- `tests/integration/artifact_postgres_test.go` atau file biometric-focused yang
  mengikuti pola test aktual;
- `internal/adapter/postgres/artifact_transactions.go` hanya bila perlu;
- `sql/queries/upload_intents.sql` dan `sql/queries/verification_artifacts.sql` hanya
  bila existing query tidak memenuhi tracer;
- schema/proof existing sebagai regression evidence.

Jangan menambah migration hanya karena Issue 008 dimulai. Schema sekarang sudah
mengizinkan kedua bounded kinds dan ownership FK.

## Bagian user

Agent lebih dulu menyiapkan scaffold satu real PostgreSQL tracer tanpa mengisi bagian
concept-bearing milik user di bawah ini.

1. `[test fixture][postgres adapter]` User membuka disposable PostgreSQL melalui
   helper existing dan mendaftarkan cleanup sesuai lifecycle test.
2. `[test fixture][postgres adapter]` User seed Verification Session berstatus
   `identity_document_uploaded` dengan token hash dan waktu expiry yang valid.
3. `[test fixture][postgres adapter]` User seed accepted Identity Document
   Verification Artifact dan parent Upload Intent yang ownership session/kind/key-nya
   valid.
4. `[test fixture][postgres adapter]` User seed pending biometric Upload Intent dengan
   fresh ID dan key `verification-sessions/{sessionID}/biometric_capture/{intentID}`.
5. `[integration test][application service]` User merakit public
   `artifact.Service.Confirm` memakai PostgreSQL adapter nyata, fake ObjectStorage,
   fail-fast extractor, production token issuer, dan fixed clock.
6. `[integration test][postgres adapter]` User memanggil `Confirm` satu kali dan
   mengassert returned summary exact `biometric_capture_uploaded`.
7. `[integration test][postgres adapter]` User membaca durable effects dan mengassert
   intent confirmed, Identity Document artifact tetap ada, tepat satu biometric
   Verification Artifact, state berubah, dan tepat satu
   `confirm_biometric_capture` event.
8. `[integration test][application service]` User mengassert HeadObject berjalan di
   luar database transaction dan DocumentExtractor tidak dipanggil.
9. `[verification]` User menjalankan tracer dan mencatat apakah hasilnya RED atau
   langsung GREEN. Direct GREEN adalah bukti existing persistence abstraction dapat
   direuse dan bukan alasan membuat query baru.
10. `[sql query][postgres adapter]` Hanya jika RED menunjukkan operasi SQL produksi
    belum ada, user menulis query minimum di `sql/queries/`, menjalankan
    `sqlc generate`, dan tidak mengedit generated file manual.
11. `[postgres adapter]` Hanya jika query baru diperlukan, user menulis mapping atau
    transaction operation minimum tanpa membocorkan pgx/SQLC type ke application.
12. `[verification]` User menjalankan tracer sampai GREEN dan memeriksa generated diff
    hanya bila SQL berubah.
13. `[review]` User berhenti dan menyerahkan tracer, perubahan minimum, serta output
    RED/GREEN kepada agent sebelum rollback dan sibling PostgreSQL cases ditambahkan.

Jika existing adapter langsung GREEN setelah application change, itu bukti reuse;
jangan membuat operasi baru demi memenuhi kuota checkpoint.

## Review agent

Agent memeriksa:

- seed merepresentasikan urutan publik yang valid;
- artifact identity dan biometric dimiliki session yang sama tetapi intent/key
  berbeda;
- transaction menggunakan shared transaction-bound queries;
- expected-state update menolak stale transition;
- unique/ownership violations diserahkan ke database, bukan hanya pre-check;
- no pgx/sqlc types keluar dari adapter;
- external HeadObject tidak berjalan saat DB transaction terbuka.

## Bagian agent

Sesudah success tracer GREEN, agent mengerjakan:

1. forced event-write failure yang membuktikan intent/artifact/state/event rollback;
2. real PostgreSQL create/supersede biometric intent dan isolation dari identity;
3. stale state/key/kind re-read dengan no partial writes;
4. constraint regressions: one artifact per intent, one accepted artifact per
   `(session, kind)`, dan ownership session/kind/key;
5. existing Identity Document PostgreSQL suite sebagai regression gate;
6. sqlc diff/generate check hanya bila SQL berubah.

## Definition of done

- user-authored PostgreSQL tracer direview dan GREEN;
- existing adapter direuse atau perubahan sempit dijelaskan oleh RED;
- atomic commit dan forced rollback terbukti;
- database constraints tetap authoritative;
- disposable PostgreSQL tests tidak memakai developer database dan tidak skip Docker;
- race/vet/sqlc gates yang relevan GREEN.

Minimum verification:

```sh
go test ./tests/integration -run 'Biometric.*Postgres|Postgres.*Biometric' -count=1 -v
go test -race ./internal/application/artifact ./internal/adapter/postgres -count=1
go vet ./internal/application/artifact ./internal/adapter/postgres ./tests/integration
```

Nama regex final harus diperbarui sesuai nama test nyata; jangan melaporkan command
hipotetis sebagai completion evidence.
