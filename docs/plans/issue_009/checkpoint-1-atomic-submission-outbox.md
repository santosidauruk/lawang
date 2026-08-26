# Checkpoint 1 — Atomic Submission dan Durable Outbox

Status: selesai pada 2026-08-26; Bagian user direview dan lulus, HMAC learning
gate Checkpoint 2 lulus, dan seluruh continuation Bagian agent GREEN.

## Tujuan

Membuktikan bahwa Provider Submission pertama mengubah Verification Session menjadi
`verification_pending`, menetapkan deadline satu hari, menulis satu
`submit_session` Session Event, dan menambahkan satu durable outbox row dalam satu
PostgreSQL transaction.

(`durable outbox` adalah row pekerjaan yang tetap tersimpan walaupun Redis atau
worker belum tersedia.) Checkpoint ini belum membuat submit HTTP route dan belum
memakai Redis.

## Perilaku tracer

Untuk satu session `biometric_capture_uploaded` yang memiliki accepted Identity
Document dan Biometric Capture Verification Artifacts:

- readiness diperiksa ulang memakai transaction-bound
  `HasRequiredAcceptedArtifacts`;
- `verification_deadline_at` menjadi exact transaction time plus 24 jam;
- state menjadi `verification_pending`;
- satu `submit_session` event ditulis dengan safe metadata;
- satu unpublished outbox row bertipe `provider:submit` dibuat dengan application-
  generated UUID dan payload identifier-only;
- forced failure sebelum commit meninggalkan keempat effect tersebut tidak berubah.

(`unpublished` berarti `published_at` masih `NULL` karena pekerjaan belum berhasil
diserahkan ke Redis/Asynq.)

## Candidate files

- `sql/migrations/00007_add_provider_submission_outbox.sql`
- `sql/queries/provider_submissions.sql`
- `sql/queries/outbox.sql`
- `sql/proofs/008_atomic_provider_submission_outbox.sql`
- `internal/application/providersubmission/service.go`
- `internal/adapter/postgres/provider_submission_transactions.go`
- focused disposable PostgreSQL tracer under `tests/integration/`

Nama package/query dapat menyesuaikan domain language yang muncul dari tracer, tetapi
jangan membuat generic job repository atau unit-of-work framework.

## Bagian user

Agent lebih dulu menyiapkan disposable PostgreSQL test runner dan success fixture
tanpa mengisi migration, query, proof, atau production transaction operation milik
user.

1. `[test fixture][postgres adapter]` User melengkapi urutan session valid sampai
   `biometric_capture_uploaded`, termasuk immutable Personal Details, kedua confirmed
   Upload Intents, kedua accepted Verification Artifacts, dan ordered Session Events.
2. `[migration]` User menambahkan nullable `verification_deadline_at` ke
   `verification_sessions` tanpa mengubah applicant `expires_at`.
3. `[migration]` User membuat tabel outbox dengan application-generated UUID,
   session ID, bounded task type `provider:submit`, identifier-only JSON payload,
   `created_at`, nullable `published_at`, `claim_token`, `claimed_until`, attempt
   count, dan bounded safe error metadata.
4. `[migration]` User menulis constraints yang mencegah empty task type/payload dan
   incoherent claim fields tanpa menyimpan Personal Details, artifact metadata, atau
   secrets di outbox.
5. `[sql query][postgres adapter]` User menulis named query untuk mengunci
   Verification Session yang diminta dan membaca state/token/deadline fields yang
   diperlukan.
6. `[sql query][postgres adapter]` User menulis named query untuk menetapkan
   `verification_pending`, exact `verification_deadline_at`, dan `updated_at` hanya
   dari expected state `biometric_capture_uploaded`.
7. `[sql query][postgres adapter]` User menulis named query pertama untuk insert satu
   unpublished `provider:submit` outbox row.
8. `[verification]` User menjalankan `sqlc generate` dan memeriksa generated diff;
   generated Go tidak diedit manual.
9. `[postgres adapter]` User menulis transaction wrapper/operations minimum yang
   memakai satu transaction-bound query set untuk lock, readiness, state update,
   Session Event append, dan outbox insert.
10. `[sql proof][sql query]` User menulis success proof bahwa state, deadline, event,
    dan outbox commit sebagai satu unit.
11. `[sql proof][sql query]` User memaksa failure setelah sebagian write dan
    membuktikan state, deadline, event, serta outbox seluruhnya rollback.
12. `[verification]` User menjalankan migration, SQL proof, dan focused PostgreSQL
    tracer sampai GREEN.
13. `[review]` User berhenti dan menyerahkan migration, query, adapter operation,
    proof, serta output kepada agent sebelum negative/concurrency cases ditambahkan.

Kesalahan readiness source, deadline ownership, transaction scope, atau outbox data
minimization adalah concept-bearing dan dikembalikan kepada user untuk direvisi.

## Review agent

Agent memeriksa:

- readiness dijalankan kembali di dalam transaction yang sama, bukan memakai bool
  snapshot sebelum transaction;
- lock/state guard terjadi sebelum writes dan replay tidak membuat row kedua;
- applicant `expires_at` tidak ditimpa oleh pending-provider deadline;
- outbox ID dibuat aplikasi dan kelak dapat menjadi exact Asynq TaskID;
- outbox payload hanya membawa identifier yang diperlukan;
- Session Event metadata tetap aman dan bounded;
- deferred rollback tidak menutupi commit error;
- claim fields mendukung Checkpoint 4 tanpa membuka transaction selama Redis I/O.

## Bagian agent

Setelah review user Checkpoint 1 lulus **dan** required HMAC learning gate Checkpoint
2 juga direview, agent menutup satu behavior per RED -> GREEN:

1. menyiapkan application-service fake/unit tests yang tidak mengharuskan user
   menulis fixture repetitif;
2. missing Identity atau Biometric Verification Artifact menggagalkan submission
   tanpa writes;
3. artifact milik session berbeda tidak memenuhi readiness;
4. wrong, terminal, dan stale state tidak menulis deadline/event/outbox;
5. forced failure pada event atau outbox insert membuktikan rollback real PostgreSQL;
6. two concurrent first submissions menghasilkan tepat satu durable transition,
   event, dan outbox;
7. generated SQLC, migration proof, adapter race tests, dan existing Issue 008
   readiness regression tetap GREEN.

Exact HTTP replay response tetap Checkpoint 3. Redis publication tetap Checkpoint 4.

## Definition of done

- user-authored migration/query/adapter operation/proof direview;
- transaction success dan rollback terbukti pada PostgreSQL disposable;
- readiness diulang di dalam transaction;
- exact one-day deadline terbukti dengan fixed time;
- one-winner concurrency menghasilkan satu outbox row;
- tidak ada Redis/provider I/O atau public route di checkpoint ini;
- focused tests, race detector, `sqlc-diff`, dan migration validation GREEN.

## Completion evidence

Bagian agent menambahkan PostgreSQL cases untuk missing accepted artifact,
wrong/non-pending dan terminal states, forced failure setelah Session Event, serta
dua first submission konkuren. Seluruh rejection/rollback case meninggalkan state,
deadline, `submit_session`, dan outbox tanpa perubahan; concurrency menghasilkan
tepat satu durable outcome dan satu caller mengenali replay setelah row lock.

Fixture Issue 008 yang memakai generated transaction-bound session query sekarang
menjalankan migration 00007 sehingga regression HTTP-to-PostgreSQL kembali GREEN.
Duplicate SQLC import pada adapter juga dihapus tanpa mengubah boundary produksi.

Verification GREEN pada 2026-08-26:

- 20 focused PostgreSQL/race/readiness regression tests;
- `make sqlc-diff`;
- `make migration-validate`;
- `git diff --check` sebelum RED scaffold Checkpoint 3 diaktifkan.

Application-service fake/unit tests yang diwajibkan poin pertama Bagian agent
menjadi RED handoff awal Checkpoint 3 karena production orchestration tetap milik
user.
