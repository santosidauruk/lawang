# Checkpoint 3 — PostgreSQL Transaction dan Adapter Boundary

Status: aktif; scaffold tracer PostgreSQL siap untuk bagian user pertama.

## Tujuan

Mengganti memory transaction dengan PostgreSQL nyata tanpa membocorkan `pgx` atau
sqlc model ke application package. Application service tetap memiliki batas
transaction; adapter hanya menyediakan mekanisme transaction dan operasi sempit yang
dikonsumsi service.

Checkpoint ini juga melengkapi persistence Verification Artifact serta persistence
untuk create/supersede Upload Intent. Presign masih fake melalui application port;
AWS SDK/MinIO nyata baru pada Checkpoint 5.

## Candidate files

Nama akhir harus mengikuti test pertama dan struktur repo aktual; jangan membuat
semuanya sekaligus.

- `sql/migrations/00006_create_verification_artifacts.sql`
- `sql/proofs/006_verification_artifact_constraints.sql`
- `sql/queries/upload_intents.sql`
- `sql/queries/verification_artifacts.sql`
- `internal/adapter/postgres/artifact_transactions.go`
- generated sqlc files di `internal/adapter/postgres/sqlc/`
- `tests/integration/artifact_postgres_test.go`
- application create-upload use case di package
  `internal/application/artifact/` setelah public test menentukan nama/shape-nya.

## Bagian user

1. Agent lebih dulu membuat satu scaffold integration test untuk success confirm
   melalui `artifact.Service.Confirm`, memakai PostgreSQL disposable dan fake
   storage/extractor. User melengkapi Arrange dan assertion sampai test RED karena
   persistence/adapter belum ada.
2. User menulis migration Verification Artifact dan constraint proof: unique Upload
   Intent, satu accepted artifact per `(session, kind)`, bounded kind, metadata
   object yang valid, dan foreign keys. Stop untuk review sebelum query ditulis.
3. Setelah schema direview, user menulis query lock/read Upload Intent pertama dan
   satu adapter operation `LockUploadIntent`. Jalankan `sqlc generate`, baca generated
   type, lalu map eksplisit ke application type. Stop untuk review.
4. User melengkapi transaction wrapper dan operasi success path hingga integration
   test pertama GREEN. External fakes harus dipanggil di luar transaction yang
   disediakan PostgreSQL adapter.

## Scaffold yang sudah disiapkan

`tests/integration/artifact_postgres_test.go` berisi satu test yang masih di-skip,
helper disposable PostgreSQL melalui migration 00001-00005, serta fake storage dan
extractor. Belum ada migration 00006, query, generated code, atau adapter artifact.

Bagian pertama user sekarang:

1. lengkapi hanya Arrange dan assertion test sukses sesuai komentar;
2. hapus `t.Skip` setelah test sudah utuh;
3. jalankan command terfokus berikut dan simpan output RED pertama:

```sh
go test ./tests/integration \
  -run '^TestPostgresArtifactConfirmPersistsAcceptedOutcomeAtomically$' -count=1
```

Compile error karena constructor/adapter PostgreSQL belum ada adalah RED yang valid.
Stop dan minta review sebelum menulis migration Verification Artifact.

User tidak perlu menulis seluruh query surface. Tujuannya adalah mengalami satu
alur: SQL -> sqlc generated type -> PostgreSQL adapter -> application port.

## Review agent

Agent memeriksa:

- tidak ada SDK/sqlc/pgx type keluar dari adapter;
- `FOR UPDATE` melindungi row yang tepat;
- `pgx.ErrNoRows` dipetakan ke expected application/domain error;
- transaction rollback aman dan `defer Rollback` tidak menutupi commit error;
- expected-state update menolak stale transition;
- artifact uniqueness adalah invariant database, bukan hanya pre-check;
- safe event metadata tidak mengandung identity number, URL, token, atau OCR body.

Kesalahan lock/transaction/constraint dikembalikan kepada user untuk direvisi.

## Kelanjutan agent

Sesudah tracer PostgreSQL GREEN, agent mengerjakan satu behavior per siklus:

1. sibling queries dan mappings untuk confirm success dan validation failure;
2. real rollback test dengan forced database error;
3. application create-upload test dengan fake presigner: authorize/read, presign di
   luar transaction, transactional re-read, supersede old pending, insert new intent;
4. PostgreSQL queries/adapter untuk supersede dan insert intent;
5. presign failure writes nothing;
6. stale re-read discards the unreturned URL and writes nothing;
7. concurrent create test membuktikan satu pending intent dan fresh unique keys.

Jangan memasukkan advisory-lock-across-I/O di sini; itu difokuskan di Checkpoint 6.

## Definition of done

- migration/proof Verification Artifact lulus di disposable PostgreSQL;
- public application success dan mismatch bekerja dengan adapter PostgreSQL nyata;
- forced failure rollback tidak meninggalkan partial intent/artifact/state/event;
- create/supersede intent atomik dan presign failure tidak menulis;
- `sqlc generate` bersih dan generated diff sudah direview;
- PostgreSQL integration tests tidak menggunakan database developer dan tidak skip
  diam-diam bila Docker unavailable.

Command minimum:

```sh
sqlc generate
go test ./tests/integration -run 'Artifact|UploadIntent' -count=1
git diff --exit-code -- internal/adapter/postgres/sqlc
```
