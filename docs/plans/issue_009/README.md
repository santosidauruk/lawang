# Issue 009 Collaboration Plan

Status: disetujui pada 2026-08-21; Bagian user Checkpoint 1 sudah direview dan
lulus, Checkpoint 2 selesai pada 2026-08-25, dan continuation Bagian agent
Checkpoint 1 tidak lagi diblokir HMAC learning gate.

Dokumen ini membagi asynchronous Provider Submission dan signed verdict callback
menjadi delapan checkpoint belajar. Setiap checkpoint menghasilkan satu perilaku
yang dapat diamati, bukan fase horizontal seperti "semua queue code" lalu "semua
webhook code".

Urutan sumber kebenaran:

1. [`docs/plan-go.md`](../../plan-go.md), terutama bagian 4-5, 8, 10, 12-14,
   16 Issue 9, dan 17.2;
2. [`docs/issues/009-submit-verification-and-apply-signed-verdicts.md`](../../issues/009-submit-verification-and-apply-signed-verdicts.md);
3. [`CONTEXT.md`](../../../CONTEXT.md);
4. hasil lengkap Issue 008 di [`docs/plans/issue_008/README.md`](../issue_008/README.md);
5. plan checkpoint dalam direktori ini.

Jika sumber tersebut bertentangan, jangan memilih diam-diam. Tunjukkan
pertentangannya dan hentikan pekerjaan yang bergantung pada keputusan tersebut.

## Hasil audit checkout sebelum penulisan plan

- Issue 008 sudah selesai dan internal
  `ArtifactTransactions.HasRequiredAcceptedArtifacts` dapat dipakai dengan database
  handle maupun `pgx.Tx` aktif. Issue 009 wajib menjalankannya kembali di dalam
  guarded submission transaction.
- domain sudah memiliki state `verification_pending`, `verified`, dan `rejected`,
  serta Session Event `submit_session`, `verification_passed`, dan
  `verification_failed`.
- belum ada outbox, Webhook Event persistence, Redis/Asynq adapter, provider adapter,
  `cmd/worker`, atau `cmd/fake-provider`.
- `verification_sessions` belum menyimpan `verification_deadline_at`, `verified_at`,
  `rejected_at`, atau `rejection_reason`.
- `compose.yaml` belum menyediakan Redis dan `go.mod` belum memiliki direct Asynq
  dependency.
- Asynq release yang diverifikasi pada 2026-08-21 adalah `v0.26.0`; pin exact version
  dan jangan mengekspos Asynq types ke application package.

## Aturan pembagian user dan agent

Issue 009 mengoreksi kecenderungan checkpoint sebelumnya yang terlalu banyak
memberikan fixture atau direct-GREEN test kepada user.

1. agent menjelaskan konsep dan menyiapkan RED test bila teknik test tersebut tidak
   memberikan pelajaran baru;
2. **Bagian user** berfokus pada application code, domain operation, SQL/query,
   adapter operation, atau runtime wiring pertama yang membawa konsep baru;
3. user menulis test sendiri hanya bila cara pembuktiannya adalah bagian inti konsep,
   yaitu SQL proof outbox, exact-raw-body HMAC test, concurrent relay proof, dan
   failure-recovery E2E yang secara eksplisit disetujui;
4. agent mereview transaction ownership, security, reliability, domain language,
   dan concrete adapter boundary;
5. kesalahan concept-bearing dikembalikan kepada user untuk direvisi;
6. **Bagian agent** dimulai setelah review dan mengambil fixture, sibling cases,
   repetitive unit tests, regression, safe error mapping, serta verification gate;
7. direct-GREEN test yang tidak membutuhkan production change adalah pekerjaan
   agent, bukan kuota coding user;
8. satu poin `Bagian user` berisi satu tindakan utama dan checkpoint selalu berhenti
   pada `[review]` sebelum continuation agent.

(`transactional outbox` adalah row pekerjaan durable yang ditulis bersama perubahan
domain di PostgreSQL lalu diserahkan ke queue setelah commit.) (`webhook` adalah
endpoint HTTP yang dipanggil provider secara otomatis saat verdict sudah tersedia.)
(`relay` adalah proses latar belakang yang memindahkan unpublished outbox rows dari
PostgreSQL ke Redis/Asynq.)

## Vocabulary tag `Bagian user`

Setiap poin `Bagian user` diawali maksimal dua tag. Tag pertama menunjukkan jenis
pekerjaan atau bukti. Tag kedua, bila ada, menunjukkan layer yang disentuh.

Jenis pekerjaan atau bukti:

- `[unit test]`: test terfokus tanpa dependency nyata, hanya bila teknik test itu
  sendiri membawa konsep baru;
- `[integration test]`: test yang melewati PostgreSQL, Redis, HTTP, MinIO, process,
  atau beberapa boundary nyata;
- `[sql proof]`: pembuktian constraint atau transaction SQL secara langsung;
- `[test fixture]`: setup dependency/fake yang hanya diberikan kepada user bila
  fixture tersebut membawa konsep baru;
- `[verification]`: menjalankan dan mencatat RED, GREEN, race detector, atau quality
  gate;
- `[review]`: titik berhenti sebelum pekerjaan sibling agent dimulai.

Layer pekerjaan:

- `[domain]`: bounded verdict, rejection reason, state, atau invariant murni;
- `[application service]`: orchestration Provider Submission, relay, atau verdict;
- `[http handler]`: request/response applicant, provider submission, atau webhook;
- `[sql query]`: SQL produksi manual di `sql/queries/`, lalu `sqlc generate`;
- `[postgres adapter]`: transaction operation dan mapping generated SQLC types;
- `[queue adapter]`: penerjemah application-owned task ke Redis/Asynq tanpa
  membocorkan Asynq types ke dalam;
- `[provider adapter]`: HTTP client yang mengirim Provider Submission melalui
  application-owned request/result;
- `[runtime wiring]`: config, constructor, process lifecycle, dan penyambungan
  dependency di `cmd/*`;
- `[migration]`: perubahan forward-only di `sql/migrations/`.

Generated files di `internal/adapter/postgres/sqlc/` tidak diedit manual. SQL
seed/assertion milik integration test memakai `[test fixture]` atau
`[integration test]`, bukan `[sql query]`.

## Delapan checkpoint yang disetujui

| # | Checkpoint | Type | Blocked by | Observable outcome | Panduan |
| --- | --- | --- | --- | --- | --- |
| 1 | Atomic submission dan durable outbox | HITL | Issue 008 | state, deadline, event, dan outbox commit/rollback bersama | [checkpoint-1](checkpoint-1-atomic-submission-outbox.md) |
| 2 | Exact-raw-body webhook HMAC | HITL | Issue 008 | invalid signature ditolak sebelum JSON decode atau persistence | [checkpoint-2](checkpoint-2-webhook-hmac-foundation.md) |
| 3 | Public submit dan exact replay | HITL | Checkpoint 1-2 review gates | `POST /submit` mengembalikan idempotent `202` tanpa menunggu Redis/provider | [checkpoint-3](checkpoint-3-public-submit.md) |
| 4 | Concurrent outbox relay ke Redis | HITL | Checkpoint 1-2 review gates | unpublished outbox diserahkan sekali secara logis dengan claim lease aman | [checkpoint-4](checkpoint-4-outbox-relay.md) |
| 5 | Signed verdict PostgreSQL outcome | HITL | Checkpoint 1-2 review gates | first valid callback atomically menerapkan verified atau rejected verdict | [checkpoint-5](checkpoint-5-signed-verdict.md) |
| 6 | Deterministic fake provider | HITL | Checkpoint 2 | proses provider terpisah mengirim signed verified/rejected callbacks | [checkpoint-6](checkpoint-6-fake-provider.md) |
| 7 | Provider submission task | HITL | Checkpoint 4 dan 6 | worker memuat immutable records lalu memanggil provider dengan stable key | [checkpoint-7](checkpoint-7-provider-task.md) |
| 8 | Full asynchronous loop dan recovery | HITL | Checkpoint 3, 5, dan 7 | verified/rejected serta outage/restart/replay terbukti end-to-end | [checkpoint-8](checkpoint-8-end-to-end-recovery.md) |

Semua checkpoint mencakup US-07. Delapan panduan ini adalah checkpoint di dalam
parent Issue 009, bukan delapan issue tracker baru.

## Keputusan yang sudah dibekukan

1. Initial submit dan exact replay sama-sama mengembalikan `202` dengan exact body:

   ```json
   {"id":"<session-uuid>","status":"verification_pending"}
   ```

2. Pending-verification TTL adalah satu hari. Transaction submission menetapkan
   `verification_deadline_at = submitted_at + 24 hours`; Issue 010 kelak memakai
   boundary `now >= verification_deadline_at` untuk expiry.
3. Provider Submission berisi `sessionId`, `callbackUrl`, immutable Personal Details,
   dan dua Verification Artifact metadata berurutan Identity Document lalu Biometric
   Capture. Ia tidak berisi raw object bytes, resume token, atau presigned URL.
4. Webhook Event sukses memiliki exact fields `eventId`, `sessionId`, dan
   `verdict: "verified"`. Rejected event menambahkan required bounded `reason`.
5. Rejection reasons tepat `document_invalid`, `biometric_mismatch`,
   `identity_not_verified`, dan `suspected_fraud`.
6. Outbox UUID menjadi stable Asynq TaskID; session UUID menjadi stable provider
   `Idempotency-Key`.
7. Outbox relay memakai short claim lease dengan `claim_token` dan `claimed_until`.
   PostgreSQL transaction ditutup sebelum enqueue ke Redis.
8. `published_at IS NULL` berarti pekerjaan belum berhasil diserahkan ke queue;
   timestamp non-null hanya berarti handoff ke Redis/Asynq selesai, bukan provider
   selesai atau verdict diterapkan.
9. Invalid signature menghasilkan `401`, tidak di-decode, tidak di-log, dan tidak
   disimpan.
10. `ignored` adalah processing status Webhook Event, bukan Verification Session
    status dan tidak membuat Session Event.

## Decision gate yang belum dibekukan

Perilaku callback dengan signature dan JSON valid tetapi `sessionId` tidak dikenal
belum disetujui. Sebelum Checkpoint 5 dimulai, user harus memilih apakah callback itu:

- disimpan sebagai `ignored` lalu mengembalikan `200`, yang mengharuskan
  `webhook_events.session_id` tidak memakai mandatory foreign key; atau
- memakai kontrak lain yang disetujui tanpa mengubah behavior late/out-of-order
  callback untuk session yang dikenal.

Jangan menulis migration Webhook Event atau unknown-session test sebelum decision
gate ini ditutup.

## Scope antark-checkpoint

- Checkpoint 1 tidak membuat HTTP route atau Redis task.
- Checkpoint 2 hanya mengautentikasi exact webhook bytes dan meneruskannya ke stub;
  ia belum menerapkan verdict.
- Checkpoint 3 berhenti setelah durable database commit dan tidak menunggu Redis.
- Checkpoint 4 hanya memindahkan task PostgreSQL -> Redis; ia tidak memanggil provider.
- Checkpoint 5 menerapkan first callback yang sudah terautentikasi tetapi tidak
  membuat fake provider; duplicate/late behavior tetap Checkpoint 8.
- Checkpoint 6 menguji provider terhadap callback recorder sebelum full Lawang loop.
- Checkpoint 7 menguji Redis task -> provider; verdict application sudah milik
  Checkpoint 5.
- Checkpoint 8 menyambungkan seluruh path dan tidak boleh menambah abstraction baru
  hanya agar test lulus.

## Reliability boundaries

- Outbox dan Asynq memberikan at-least-once delivery (pekerjaan dapat dicoba lebih
  dari sekali, sehingga setiap effect harus idempotent), bukan exactly-once.
- `claim_token` mencegah relay lama menandai row sesudah lease-nya diambil relay baru.
- claim expiry membuat row dapat direbut kembali setelah relay mati.
- duplicate TaskID adalah successful publication, tetapi stable provider
  `Idempotency-Key` dan Webhook Event deduplication tetap diperlukan.
- maksimal sepuluh provider attempts yang habis tidak berarti applicant ditolak;
  session tetap `verification_pending` sampai callback atau expiry Issue 010.
- semua external network I/O terjadi di luar PostgreSQL transaction.

## Completion contract

Issue 009 selesai hanya bila:

- dua required user learning gates direview;
- seluruh delapan checkpoint selesai sesuai ownership;
- dependency-backed tests tidak `t.Skip` ketika Docker unavailable;
- focused PostgreSQL, Redis, provider, webhook, dan full E2E suites GREEN;
- `make quality`, `make sqlc-diff`, migration validation, dan Compose validation
  GREEN;
- [`docs/learning/009-outbox-and-signed-webhooks.md`](../../learning/009-outbox-and-signed-webhooks.md)
  mencatat keputusan serta command/output aktual tanpa klaim exactly-once.
