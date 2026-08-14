# Checkpoint 3 — PostgreSQL Atomic Biometric Outcome

Status: selesai pada 2026-08-14; tracer user, review gate, Bagian agent, regression,
dan full quality gate GREEN.

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

## Tracer yang disiapkan

Tracer user berada di
`tests/integration/artifact_biometric_postgres_test.go` sebagai
`TestBiometricConfirmPersistsPostgresOutcomeAtomically`.

Tracer sekarang berisi disposable PostgreSQL fixture, accepted Identity Document
prerequisite, pending Biometric Capture intent, service dengan adapter/coordinator
PostgreSQL nyata, satu call `Confirm`, durable outcome assertions, serta test-only
transaction observer untuk membuktikan `HeadObject` tidak berjalan saat transaction
database aktif. Extractor dibuat fail-fast dan harus tetap memiliki nol call.

Focused PostgreSQL tracer direct GREEN. Existing query dan adapter dapat direuse
tanpa perubahan, sehingga poin 10-11 tidak diperlukan. Poin 12 sudah terpenuhi oleh
run GREEN yang sama; generated diff tidak diperlukan karena tidak ada SQL yang
berubah. Belum ada query, adapter, migration, atau generated SQLC yang diubah.

Focused command selama bagian user:

```sh
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run '^TestBiometricConfirmPersistsPostgresOutcomeAtomically$' -count=1 -v
```

Gunakan `openArtifactDatabase(t)` dan pola fixture dalam
`tests/integration/artifact_postgres_test.go`; jangan membuat container helper,
adapter, schema, atau query paralel sebelum RED nyata membuktikan gap.

Verification evidence poin 9 pada 2026-08-14:

```text
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run '^TestBiometricConfirmPersistsPostgresOutcomeAtomically$' -count=1 -v
--- PASS: TestBiometricConfirmPersistsPostgresOutcomeAtomically (7.47s)
PASS
ok github.com/santosidauruk/lawang-go/tests/integration 8.581s
```

Keputusan sesudah direct GREEN:

- poin 10 `[sql query][postgres adapter]`: tidak dikerjakan karena tidak ada missing
  production SQL operation;
- poin 11 `[postgres adapter]`: tidak dikerjakan karena existing mapping dan
  transaction operations sudah memenuhi tracer;
- poin 12 `[verification]`: selesai; tidak ada SQL/generated file yang perlu dicek
  ulang melalui `sqlc generate`;
- poin 13 `[review]`: selesai; review gate menyetujui tracer setelah fixture history
  diperbaiki.

## Bagian user — selesai dan direview

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

Existing adapter langsung GREEN. Tidak ada query, mapping, migration, atau generated
SQLC baru yang dibuat hanya untuk memenuhi kuota checkpoint.

## Hasil review agent

Review awal menemukan satu gap test fixture: state
`identity_document_uploaded` sudah memiliki confirmed intent, Verification Artifact,
dan event konfirmasi, tetapi belum menyimpan immutable Personal Details serta event
`submit_personal_details` yang membuat urutan publiknya lengkap. Fixture diperbaiki;
focused tracer tetap GREEN sesudah koreksi.

Review akhir mengonfirmasi:

- seed merepresentasikan urutan publik yang valid;
- artifact identity dan biometric dimiliki session yang sama tetapi intent/key
  berbeda;
- transaction menggunakan shared transaction-bound queries;
- expected-state update menolak stale transition;
- unique/ownership violations diserahkan ke database, bukan hanya pre-check;
- no pgx/sqlc types keluar dari adapter;
- external HeadObject tidak berjalan saat DB transaction terbuka.

## Bagian agent — selesai

Sesudah success tracer GREEN dan direview, agent menyelesaikan:

1. `TestBiometricPostgresConfirmRollsBackWhenEventWriteFails` membuktikan forced
   event-write failure me-rollback intent, biometric artifact, state, dan biometric
   event tanpa menghapus Identity Artifact;
2. `TestBiometricPostgresCreateSupersedesOnlyPendingBiometricIntent` membuktikan real
   PostgreSQL create/supersede menyisakan satu pending biometric intent dan tidak
   mengubah confirmed Identity outcome;
3. `TestBiometricPostgresConfirmRejectsStaleRereadWithoutPartialWrites` membuktikan
   perubahan state, key, dan kind saat `HeadObject` menghasilkan
   `CONFIRMATION_STALE` tanpa partial writes;
4. existing `TestVerificationArtifactMigrationAndConstraintProof` membuktikan satu
   artifact per intent, satu artifact per `(session, kind)`, bounded metadata/kind,
   dan ownership session/kind/key melalui exact PostgreSQL constraints;
5. seluruh existing Identity Document PostgreSQL success, mismatch, rollback,
   create/supersede, dan concurrent-create tests tetap GREEN;
6. `make sqlc-diff` GREEN tanpa generated diff karena production SQL tidak berubah.

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

Final verification evidence pada 2026-08-14:

```text
GOCACHE=/tmp/lawang-go-build go test ./tests/integration \
  -run 'Biometric.*Postgres|Postgres.*Biometric' -count=1 -v
PASS: success, forced rollback, create/supersede isolation, and three stale re-reads
ok github.com/santosidauruk/lawang-go/tests/integration 21.794s

GOCACHE=/tmp/lawang-go-build go test ./tests/schema \
  -run '^TestVerificationArtifactMigrationAndConstraintProof$' -count=1 -v
NOTICE: proof passed: Verification Artifact constraints
ok github.com/santosidauruk/lawang-go/tests/schema 8.656s

GOCACHE=/tmp/lawang-go-build go test -race \
  ./internal/application/artifact ./internal/adapter/postgres -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact
ok github.com/santosidauruk/lawang-go/internal/adapter/postgres

GOCACHE=/tmp/lawang-go-build go vet \
  ./internal/application/artifact ./internal/adapter/postgres ./tests/integration
PASS

GOCACHE=/tmp/lawang-go-build make sqlc-diff
PASS: no generated diff

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate
```
