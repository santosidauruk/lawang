# Checkpoint 3 — PostgreSQL Transaction dan Adapter Boundary

Status: selesai; seluruh bagian user dan agent continuation GREEN pada PostgreSQL
disposable.

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

`tests/integration/artifact_postgres_test.go` memiliki tracer accepted outcome yang
ditulis user serta sibling mismatch dan forced-rollback yang ditulis agent. Ketiganya
berjalan melalui public `artifact.Service.Confirm` dan PostgreSQL disposable.

Scaffold bagian schema berada di:

- `sql/migrations/00006_create_verification_artifacts.sql`;
- `sql/proofs/006_verification_artifact_constraints.sql`;
- `tests/schema/verification_artifacts_postgres_test.go` sebagai runner agent-owned.

Bagian schema user sudah selesai dan direview. Migration serta proof lulus pada
PostgreSQL `18.4-alpine3.23` disposable pada 2026-07-23.

Jalankan satu skenario proof pada satu waktu melalui PostgreSQL disposable:

```sh
docker desktop status
go test ./tests/schema \
  -run '^TestVerificationArtifactMigrationAndConstraintProof$' -count=1 -v
```

Runner gagal bila migration belum membuat tabel, psql menemukan violation yang tidak
ditangani, atau proof belum mengeluarkan completion marker yang ditentukan scaffold.

Migration/proof harus membuktikan unique Upload Intent, satu artifact per
`(session, kind)`, bounded kind, valid generic object metadata, foreign keys, serta
kesesuaian ownership session/kind dengan Upload Intent. Mapping JPEG/PNG/PDF dan batas
10 MiB tetap milik application code.

`docs/plan-go.md` menyebut minimized extraction result, tetapi application type saat
ini belum mendefinisikan field bounded tersebut. Jangan membuat kolom spekulatif;
bawa gap ini ke review sebelum public application type diperluas.

Bagian user langkah 3 sudah selesai dan direview: `LockUploadIntent :one` mengunci
row yang dimiliki Verification Session yang diminta; generated params bernama jelas;
adapter memetakan seluruh row, nullable pgtype, dan `pgx.ErrNoRows` tanpa membocorkan
type sqlc ke application.

Bagian user langkah 4 sudah selesai dan direview. Adapter memenuhi
`artifact.Reader`, `artifact.Transactor`, dan transaction-scoped
`artifact.Transaction`; external fakes tetap dipanggil sebelum transaction dibuka.
Implementasinya berada di:

- `sql/queries/upload_intents.sql` untuk non-locking read serta expected-state
  updates;
- `sql/queries/verification_artifacts.sql` untuk insert accepted artifact;
- `internal/adapter/postgres/artifact_transactions.go` untuk constructor, Reader,
  transaction wrapper, dan transaction operations.

Review agent mengonfirmasi query lock memakai ownership session/intent yang tepat,
non-transaction Reader memakai pool-bound queries, transaction memakai satu shared
transaction-bound queries value, affected-row stale menjadi bounded application
error, dan commit error tidak ditutupi deferred rollback.

Agent continuation yang selesai:

1. mismatch nyata meng-commit `validation_failed`, bounded failure code, dan satu
   safe event tanpa artifact atau state transition;
2. trigger PostgreSQL yang memaksa event-write failure membuktikan intent confirmation,
   artifact insert, state transition, dan event seluruhnya rollback.

Command focused yang GREEN:

```sh
go test ./tests/integration \
  -run '^TestPostgresArtifactConfirm(PersistsAcceptedOutcomeAtomically|PersistsMismatchOutcomeAtomically|RollsBackAcceptedOutcomeWhenEventWriteFails)$' \
  -count=1 -v
```

Create/supersede continuation selesai dengan storage key yang disetujui:
`verification-sessions/{sessionID}/identity_document/{intentID}`. Application test
membuktikan presign berjalan sebelum transaction, TTL lima menit, presign failure
tidak menulis, dan stale transactional re-read tidak mengembalikan URL atau menulis.
PostgreSQL integration test membuktikan replacement atomik serta tepat satu pending
intent dan fresh unique key saat dua create berjalan concurrent.

Final checkpoint verification yang GREEN pada 2026-07-24:

```sh
go test -race ./internal/application/artifact ./internal/adapter/postgres
go vet ./internal/application/artifact ./internal/adapter/postgres ./tests/integration
make sqlc-diff
go test ./tests/integration -run 'Artifact|UploadIntent' -count=1 -v
go test ./tests/schema \
  -run '^TestVerificationArtifactMigrationAndConstraintProof$' -count=1 -v
```

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
