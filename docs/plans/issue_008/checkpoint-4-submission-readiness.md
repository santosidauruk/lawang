# Checkpoint 4 — Artifact-Derived Submission Readiness

Status: approved; menunggu agent scaffold setelah Checkpoint 3 selesai 2026-08-14.

## Tujuan

Membuktikan predicate persistence (pemeriksaan kondisi berdasarkan data tersimpan
yang menghasilkan jawaban ya/tidak) bahwa Provider Submission hanya eligible bila
Verification Session yang sama memiliki accepted Identity Document dan accepted
Biometric Capture Verification Artifact.

Checkpoint ini tidak membuat `/submit`, tidak mengubah state ke
`verification_pending`, dan tidak membuat outbox. Semua itu tetap Issue 009.

## Risiko yang harus disadari

Nilai standalone `ready=true` dapat menjadi stale sebelum submit transaction dimulai.
Karena itu keputusan yang disetujui adalah internal SQL-backed predicate yang kelak
dipanggil di dalam guarded transaction Issue 009, bukan public readiness endpoint
atau klaim bahwa check ini sendiri mengotorisasi submission.

## Perilaku tracer

Predicate hanya true bila dua accepted Verification Artifact dengan exact kinds
`identity_document` dan `biometric_capture` dimiliki Verification Session yang sama.
Ia false untuk:

- stored objects tanpa artifact;
- pending/confirmed/validation-failed Upload Intent tanpa accepted artifact;
- hanya salah satu kind;
- dua artifact yang masing-masing dimiliki session berbeda;
- duplicated or arbitrary rows yang tidak lolos schema ownership constraints.

Session state boleh dipakai sebagai guard tambahan oleh Issue 009, tetapi tidak boleh
menjadi pengganti artifact predicate.

## Candidate files

- `sql/queries/verification_artifacts.sql`
- `sql/proofs/006_verification_artifact_constraints.sql` atau proof baru dengan nomor
  forward berikutnya bila pemisahan membuat evidence lebih jelas;
- `internal/adapter/postgres/artifact_transactions.go` atau adapter boundary sempit
  yang muncul dari tracer;
- integration test PostgreSQL untuk readiness predicate.

Nama interface/result belum dibekukan. Ia harus muncul dari tracer setelah user
menyetujui boundary internal-only.

## Bagian user

Tidak ada public readiness endpoint atau field di Issue 008; keputusan ini sudah
disetujui. Agent lebih dulu menyiapkan fixture PostgreSQL minimal tanpa mengisi query
atau assertion concept-bearing milik user.

1. `[test fixture][postgres adapter]` User seed satu Verification Session dengan
   accepted Identity Document dan accepted Biometric Capture Verification Artifact
   yang ownership session/kind/key-nya valid.
2. `[integration test][postgres adapter]` User menulis satu tracer melalui internal
   readiness boundary yang meminta jawaban untuk exact Verification Session ID.
3. `[integration test][postgres adapter]` User mengassert readiness `true` hanya dari
   dua accepted Verification Artifact untuk session yang sama, tanpa membaca stored
   object atau mengandalkan Upload Intent status saja.
4. `[verification]` User menjalankan tracer dan mencatat RED karena production query
   atau adapter boundary belum tersedia.
5. `[sql query][postgres adapter]` User menulis query readiness minimum di
   `sql/queries/verification_artifacts.sql`, dengan exact filter untuk kedua bounded
   kinds dan Verification Session ID yang sama.
6. `[verification]` User menjalankan `sqlc generate` dan memeriksa generated diff;
   generated Go file tidak diedit manual.
7. `[postgres adapter]` User mengekspos generated query melalui internal boundary yang
   dapat dipakai dengan database handle atau transaction-bound handle.
8. `[sql proof][sql query]` User menulis success proof minimum bahwa dua accepted
   kinds pada session yang sama menghasilkan `true`; negative matrix tetap milik
   kelanjutan agent setelah review.
9. `[verification]` User menjalankan integration tracer dan SQL proof sampai GREEN.
10. `[review]` User berhenti dan menyerahkan query, boundary, proof, serta output
    RED/GREEN kepada agent sebelum false-positive matrix ditambahkan.

Kesalahan pada same-session semantics atau perbedaan intent/object/artifact adalah
concept-bearing dan dikembalikan kepada user untuk direvisi.

## Review agent

Agent memeriksa:

- query menggunakan accepted Verification Artifact rows, bukan Upload Intent status;
- kedua kinds harus dimiliki session parameter yang sama;
- SQL tidak bergantung pada object presence atau session state saja;
- boundary dapat dipakai transaction-bound oleh Issue 009;
- bool/result tidak dipresentasikan sebagai durable reservation;
- index/constraint existing cukup; migration baru hanya jika query plan/invariant
  membuktikan kebutuhan nyata.

## Bagian agent

Sesudah success predicate GREEN, agent menambah satu negative behavior per siklus:

1. identity-only dan biometric-only;
2. stored-object fixture tanpa accepted artifact;
3. confirmed biometric intent tanpa accepted artifact;
4. validation-failed identity intent plus biometric artifact;
5. artifacts tersebar pada dua sessions;
6. regression proof terhadap schema ownership dan unique `(session, kind)`;
7. query plan/quality gate yang relevan dan documentation handoff untuk Issue 009.

## Definition of done

- readiness boundary disetujui user;
- user-authored success query/tracer direview;
- semua false-positive fixtures di acceptance criteria terbukti false;
- tidak ada route, response field, submit transition, atau outbox yang bocor dari
  Issue 009;
- dokumentasi menyatakan predicate harus diulang di guarded submit transaction.

Minimum verification harus memakai nama test/proof aktual dan PostgreSQL disposable.
