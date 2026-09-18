# Issue 010 Collaboration Plan

Status: disetujui pada 2026-09-16; Checkpoint 1 aktif dan scaffold agent RED sudah
siap untuk Bagian user.

Dokumen ini membagi operability MVP menjadi delapan checkpoint belajar. Setiap
checkpoint menghasilkan satu perilaku yang dapat diamati dan memiliki `Bagian user`,
gate `[review]`, serta `Bagian agent`; delapan checkpoint ini bukan delapan issue
tracker baru.

(`operability` adalah kemampuan sistem untuk dijalankan, diperiksa, dipulihkan, dan
dirawat melalui surface operasional yang terdokumentasi.) (`vertical checkpoint`
adalah potongan kerja yang menembus layer yang diperlukan sampai menghasilkan satu
perilaku yang dapat diamati.)

Urutan sumber kebenaran:

1. [`docs/plan-go.md`](../../plan-go.md), terutama bagian 5.3, 9.5, 10-16,
   16 Issue 10, 17.2, dan 20;
2. [`docs/issues/010-operate-expiry-cleanup-audit-and-mvp.md`](../../issues/010-operate-expiry-cleanup-audit-and-mvp.md);
3. [`CONTEXT.md`](../../../CONTEXT.md);
4. hasil lengkap Issue 009 di [`docs/plans/issue_009/README.md`](../issue_009/README.md);
5. plan checkpoint dalam direktori ini.

Jika sumber tersebut bertentangan, jangan memilih diam-diam. Tunjukkan
pertentangannya dan hentikan pekerjaan yang bergantung pada keputusan tersebut.

## Hasil audit checkout sebelum penulisan plan

- Issue 009 berstatus `done`, checkout berada pada commit `6af186a`, dan learning
  note Issue 009 mencatat full quality gate exit `0`.
- domain sudah memiliki public state `expired`, Session Event `expire`, dan operasi
  `verificationsession.Expire`; checkpoint tidak boleh menulis ulang konsep tersebut.
- schema sudah memiliki `verification_deadline_at`, Upload Intent statuses
  `superseded|expired|validation_failed`, dan nullable `object_deleted_at`.
- application code saat ini hanya mengembalikan `UPLOAD_INTENT_EXPIRED` tanpa
  mempersist status Upload Intent `expired`; Checkpoint 3 menutup gap ini sebelum
  cleanup memilih kandidat.
- `/health/live`, safe request logging, safe provider-task logging, API/worker/fake-
  provider images, dan Compose topology sudah ada; Issue 010 mengaudit serta
  melengkapi, bukan membuat ulang.
- belum ada shared expiry application operation, scheduled expiry/cleanup task,
  cleanup use case, audit CLI, `/health/ready`, `docs/openapi.yaml`, atau CI workflow.

## Aturan pembagian user dan agent

1. agent menjelaskan konsep serta menyiapkan fixture dan RED tracer bila teknik test
   tidak memberikan pelajaran baru;
2. **Bagian user** berfokus pada application code, handwritten SQL, adapter
   operation, dan runtime wiring pertama yang membawa konsep baru;
3. user tidak menulis unit, integration, race, atau regression test repetitif;
4. satu-satunya proof seperti test yang dimiliki user adalah cleanup-safety SQL proof
   Checkpoint 3 karena teknik structural exclusion adalah konsep inti;
5. agent mereview transaction ownership, concurrency, data minimization, recovery,
   public contract, dan concrete adapter boundary;
6. kesalahan concept-bearing dikembalikan kepada user untuk direvisi;
7. **Bagian agent** dimulai setelah focused GREEN dan gate `[review]` lulus;
8. satu poin `Bagian user` berisi satu tindakan utama dan tepat satu tag;
9. setiap poin `Bagian user` menjelaskan urutan sederhana: apa yang dibaca atau
   disiapkan, perubahan yang ditulis, lalu hasil yang harus diperiksa;
10. `[sql query]` dan `[postgres adapter]` selalu menjadi poin terpisah;
11. `[queue adapter]` dan `[runtime wiring]` selalu menjadi poin terpisah;
12. generated files di `internal/adapter/postgres/sqlc/` tidak diedit manual.

(`application code` adalah kode produksi yang menjalankan aturan atau orchestration
use case, bukan kode test.) (`sibling case` adalah variasi perilaku yang memakai
konsep produksi yang sama setelah success path pertama terbukti.) (`regression test`
adalah test yang menjaga perilaku yang sudah benar agar tidak rusak oleh perubahan
berikutnya.) (`review gate` adalah titik berhenti wajib sebelum agent melanjutkan
reliability, safety, dan regression work.)

## Vocabulary tag

Setiap poin `Bagian user` memakai tepat satu tag agar satu tindakan tidak mencampur
dua layer.

Tag pekerjaan atau bukti:

- `[sql proof]`: pembuktian invariant SQL secara langsung dengan setup dan assertion
  yang dapat dijalankan ulang;
- `[verification]`: menjalankan dan mencatat RED, GREEN, generated diff, race
  detector, atau quality gate;
- `[review]`: titik berhenti sebelum pekerjaan agent dimulai;
- `[api contract]`: review method, auth, field, status, replay, conflict, dan error
  publik yang harus cocok dengan implementation evidence;
- `[documentation]`: penulisan runbook, limitation, atau evidence tanpa mengubah
  runtime behavior;
- `[ci]`: pipeline otomatis yang menjalankan quality dan dependency-backed gates.

Tag layer:

- `[domain]`: state, event, deadline rule, dan invariant murni tanpa dependency I/O;
- `[application service]`: orchestration use case melalui application-owned ports;
- `[sql query]`: SQL produksi manual di `sql/queries/`, lalu diregenerasi dengan
  sqlc;
- `[postgres adapter]`: transaction operation dan mapping generated SQLC types ke
  application types;
- `[object storage adapter]`: penerjemah application-owned object operation ke API
  S3-compatible;
- `[queue adapter]`: penerjemah application-owned task ke Redis/Asynq tanpa
  membocorkan Asynq types ke application package;
- `[http handler]`: penerjemah request/response HTTP ke application operation;
- `[observability]`: pencatatan dan korelasi runtime tanpa membocorkan data sensitif;
- `[runtime wiring]`: config, constructor, process lifecycle, scheduler, dan
  penyambungan dependency di `cmd/*`.

## Istilah baru lintas checkpoint

- `lazy expiry` adalah expiry yang diperiksa saat request applicant mengakses session
  yang sudah melewati deadline.
- `scheduled sweep` adalah pekerjaan berkala yang mencari record overdue walaupun
  tidak ada request applicant.
- `convergence` adalah dua jalur eksekusi berbeda yang berakhir pada satu durable
  state dan satu domain effect yang sama.
- `grace period` adalah jeda 24 jam setelah Upload Intent tidak lagi aktif sebelum
  object-nya boleh menjadi kandidat cleanup.
- `redaction` adalah penghilangan field sensitif dari output atau log sebelum data
  ditampilkan.
- `liveness` adalah pemeriksaan bahwa process masih merespons tanpa melakukan
  dependency I/O.
- `readiness` adalah pemeriksaan bahwa dependency wajib process dapat dijangkau dalam
  batas waktu tertentu.
- `correlation ID` adalah identifier aman yang menghubungkan log dari satu request
  atau task tanpa membawa payload sensitif.
- `contract parity` adalah keadaan ketika OpenAPI, handler, dan integration evidence
  mendeskripsikan perilaku publik yang sama.
- `fresh clone` adalah checkout baru tanpa build cache, container volume, atau file
  konfigurasi dari workspace lama.

Istilah yang sudah didefinisikan di dokumen sumber tidak diulang. Setiap checkpoint
memiliki bagian `Istilah baru` sendiri dan menjelaskan setiap istilah teknis bahasa
Inggris baru dalam tepat satu kalimat pada kemunculan pertamanya.

## Delapan checkpoint yang disetujui

| # | Checkpoint | Blocked by | Observable outcome | Panduan |
| --- | --- | --- | --- | --- |
| 1 | Guarded Expiry Operation | Issue 009 | satu overdue session menjadi `expired` dengan satu event `expire` dalam satu transaction | [checkpoint-1](checkpoint-1-guarded-expiry-operation.md) |
| 2 | Lazy dan Scheduled Expiry Convergence | Checkpoint 1 review | request applicant dan Asynq sweep memakai expiry operation yang sama | [checkpoint-2](checkpoint-2-lazy-and-scheduled-expiry.md) |
| 3 | Safe Orphan Selection dan Manual Cleanup | Checkpoint 2 review | production query hanya memilih object yang aman lalu manual command menghapusnya idempotently | [checkpoint-3](checkpoint-3-safe-orphan-selection-and-manual-cleanup.md) |
| 4 | Scheduled Cleanup dan Failure Recovery | Checkpoint 3 review | scheduled task dan manual command memanggil cleanup use case yang sama | [checkpoint-4](checkpoint-4-scheduled-cleanup-and-recovery.md) |
| 5 | Redacted Audit CLI | Checkpoint 4 review | local CLI mencetak state dan ordered safe event history | [checkpoint-5](checkpoint-5-redacted-audit-cli.md) |
| 6 | Readiness dan Safe Observability | Checkpoint 5 review | liveness tetap tanpa I/O, readiness bounded, dan log dapat dikorelasikan dengan aman | [checkpoint-6](checkpoint-6-readiness-and-safe-observability.md) |
| 7 | OpenAPI Contract Parity | Checkpoint 1-6 contracts | OpenAPI Issue 001-010 cocok dengan public behavior aktual | [checkpoint-7](checkpoint-7-openapi-contract-parity.md) |
| 8 | Operable MVP dan Fresh-Clone Proof | Checkpoint 1-7 | checkout baru dapat bootstrap dan menjalankan full documented demo | [checkpoint-8](checkpoint-8-operable-mvp-and-fresh-clone.md) |

Semua checkpoint mencakup US-08 dan dikerjakan secara berurutan. Full quality gate
dijalankan pada Checkpoint 8; checkpoint sebelumnya memakai focused verification
yang tertulis di panduan masing-masing.

## Keputusan yang dibekukan

1. Applicant-stage session memakai `expires_at`; hanya `verification_pending` yang
   memakai non-null `verification_deadline_at`.
2. Boundary expiry adalah `now >= applicable deadline`.
3. Lazy path dan scheduled path memanggil satu application-owned expiry operation;
   keduanya tidak menyalin state-update/event logic.
4. Authentication diselesaikan sebelum lazy expiry memutasi session sehingga token
   invalid tidak boleh memicu expiry side effect.
5. Satu transaction mengunci session, guarded-update ke `expired`, dan menulis satu
   event `expire`; terminal session tidak berubah.
6. Pending Upload Intent dengan `expires_at <= now` dimaterialisasi menjadi
   `expired`; logical transition time-nya adalah `expires_at`, bukan waktu cleanup
   kebetulan berjalan.
7. Cleanup candidate hanya berstatus `superseded|expired|validation_failed`, melewati
   grace period 24 jam, `object_deleted_at IS NULL`, dan tidak direferensikan oleh
   Verification Artifact.
8. Object deletion berjalan di luar transaction PostgreSQL; marker ditulis melalui
   guarded update sesudah delete atau object-not-found yang idempotent.
9. `session:expire` dan `storage:cleanup` adalah scheduled Asynq tasks dan tidak masuk
   transactional outbox karena keduanya dapat dibuat ulang oleh scheduler.
10. Audit adalah local CLI, bukan HTTP route, dan mengambil explicit safe columns
    saja.
11. `/health/ready` memeriksa PostgreSQL serta MinIO bucket karena keduanya adalah
    dependency langsung API; Redis dan provider bukan dependency request API.
12. Readiness success adalah exact `200 {"status":"ready"}` dan failure adalah exact
    `503 {"status":"not_ready"}` tanpa nama dependency atau internal error.
13. Correlation memakai request ID, TaskID, dan session ID yang sudah aman; jangan
    memperbesar outbox atau task payload dengan data sensitif.
14. Fresh-clone proof membutuhkan seluruh checkpoint sebelumnya sudah berada dalam
    commit; agent tidak membuat commit tanpa instruksi user.

## Scope antark-checkpoint

- Checkpoint 1 belum mengubah HTTP route atau membuat scheduled task.
- Checkpoint 2 menyelesaikan seluruh session expiry surface tetapi belum menghapus
  object storage.
- Checkpoint 3 membuktikan selection safety dan manual cleanup tanpa scheduler.
- Checkpoint 4 menambahkan scheduled cleanup tanpa membuat cleanup use case kedua.
- Checkpoint 5 membaca data audit tanpa memperkenalkan admin HTTP API.
- Checkpoint 6 melengkapi health/logging tanpa menambah metrics atau OpenTelemetry.
- Checkpoint 7 mendokumentasikan contract yang sudah dibekukan; ia tidak mengubah
  application behavior hanya agar OpenAPI terlihat benar.
- Checkpoint 8 menyambungkan command, image, CI, dan E2E tanpa menambah domain
  abstraction baru hanya agar demo lulus.

## Completion contract

Issue 010 selesai hanya bila:

- cleanup-safety SQL proof user direview dan lulus;
- OpenAPI review user selesai sebelum agent menutup repetitive schema/examples;
- seluruh delapan checkpoint selesai sesuai ownership dan gate `[review]`;
- dependency-backed tests tidak memakai silent skip;
- full quality, migration, SQLC diff, Compose, Docker build, integration, dan E2E
  gates GREEN;
- fresh-clone bootstrap/demo berhasil tanpa volume atau cache workspace lama;
- [`docs/learning/010-operable-mvp.md`](../../learning/010-operable-mvp.md) mencatat
  keputusan, command/output aktual, serta production limitations tanpa klaim
  production-ready.
