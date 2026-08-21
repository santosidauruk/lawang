# Checkpoint 8 — Full Asynchronous Loop dan Failure Recovery

Status: menunggu Checkpoint 3, 5, dan 7.

## Tujuan

Membuktikan seluruh jalur applicant API -> PostgreSQL outbox -> Redis/Asynq -> worker
-> fake provider -> signed webhook -> terminal Verification Session memakai
dependency nyata dan restart/replay behavior yang aman.

(`end-to-end` berarti test menjalankan seluruh jalur publik serta dependency nyata,
bukan mengganti boundary penting dengan in-process fake.) Checkpoint ini tidak boleh
membuat abstraction production baru kecuali failing behavior membuktikan gap nyata.

## Perilaku tracer

Success flow:

1. applicant membuat session dan Personal Details;
2. Identity Document dan Biometric Capture di-upload/confirm melalui PostgreSQL dan
   real MinIO;
3. public submit mengembalikan exact `202` setelah durable outbox commit;
4. relay mempublikasikan task ke real Redis;
5. worker memanggil separate fake provider;
6. provider mengirim exact signed Webhook Event;
7. Lawang menerapkan verified atau rejected verdict atomically.

Recovery flow membuktikan Redis/provider restart tidak menghilangkan pekerjaan dan
duplicate/late callbacks tidak mengulang domain transition.

## Candidate files

- `tests/e2e/provider_verification_test.go` atau cohesive existing integration package
- process/test harness untuk API, worker, dan fake provider
- Compose/Makefile commands khusus E2E
- `docs/learning/009-outbox-and-signed-webhooks.md` pada completion

## Bagian user

Agent menyiapkan reusable process/container harness dan baseline verified/rejected E2E
tests. User berfokus pada recovery/replay experiments yang secara eksplisit dipilih,
bukan menulis ulang setup umum.

1. `[integration test][runtime wiring]` User menjalankan baseline verified flow dan
   menelusuri exact IDs: Verification Session, outbox TaskID, provider
   Idempotency-Key, serta Webhook Event ID.
2. `[integration test][queue adapter]` User menulis Redis-restart scenario: submit
   commit ketika Redis unavailable, pastikan response tetap `202`, hidupkan Redis/
   worker kembali, lalu buktikan unpublished work akhirnya diproses.
3. `[integration test][provider adapter]` User menulis provider-restart scenario:
   provider unavailable menyebabkan task retry tanpa rejection, lalu restart provider
   dan buktikan stable Idempotency-Key menghasilkan satu logical submission outcome.
4. `[integration test][http handler]` User menulis exact duplicate-callback scenario
   dengan same event ID/body dan mengassert `200` tanpa state, terminal timestamp,
   Session Event, atau Webhook Event row kedua.
5. `[verification]` User menjalankan duplicate scenario dan mencatat RED bila
   application service belum mengembalikan recorded outcome secara idempotent.
6. `[application service]` Jika RED, user menulis minimum duplicate branch yang
   mengenali stored event ID dan mengembalikan success tanpa domain write baru.
7. `[integration test][http handler]` User menulis late-callback scenario dengan event
   ID berbeda setelah terminal state dan mengassert durable `ignored` outcome tanpa
   state/Session Event tambahan.
8. `[verification]` User menjalankan late scenario dan mencatat RED bila callback
   valid masih mencoba terminal transition atau belum dapat disimpan `ignored`.
9. `[application service]` Jika RED, user menulis minimum late branch dan PostgreSQL
   operation yang menyimpan bounded `ignored` outcome tanpa Session Event.
10. `[verification]` User menjalankan keempat recovery/replay scenarios dengan bounded
   timeout dan tanpa `time.Sleep` sebagai correctness proof.
11. `[verification]` User memeriksa real Redis/provider restart benar-benar terjadi
   dan test tidak menggantinya dengan no-op atau silently skipped dependency.
12. `[review]` User berhenti dan menyerahkan application/recovery code, trace ID
    evidence, serta
   output sebelum agent menutup full matrix.

Kesalahan test yang hanya merestart client object, duplicate dengan event ID baru,
late callback yang membuat Session Event, atau outage yang diam-diam menjadi applicant
rejection adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- test memakai public API dan real dependency boundaries;
- Redis restart terjadi setelah durable PostgreSQL commit;
- provider restart mempertahankan same logical idempotency key;
- duplicate vs late event dibedakan oleh event ID;
- bounded waits memakai observable readiness/events, bukan arbitrary sleeps;
- terminal state tidak berubah dan ignored tetap Webhook Event processing status;
- no exactly-once claim: evidence hanya menunjukkan idempotent effect under tested
  at-least-once delivery.

## Bagian agent

Setelah recovery/replay user scenarios direview, agent menutup:

1. full verified flow dengan real PostgreSQL, MinIO, Redis, API, worker, dan provider;
2. full rejected flow untuk all four bounded reasons, reusing user-owned Checkpoint 5
   behavior tanpa menulis ulang core verdict code;
3. relay crash before/after enqueue, worker restart during active/retry task, dan API/
   provider process shutdown regressions;
4. concurrent task/callback stress yang tetap bounded dan deterministic; concurrent
   relay proof direuse dari user-owned Checkpoint 4 tanpa menulis ulang tracer itu;
5. invalid-signature E2E membuktikan no Webhook Event/session mutation;
6. safe logs tidak memuat tokens, Authorization, presigned URLs, Personal Details,
   artifact metadata, webhook bodies, atau secrets;
7. focused dependency-backed suites, race detector, `sqlc-diff`, migration/Compose
   validation, dan full `make quality` tanpa silent skip;
8. learning note dengan actual commands, container versions, recovery evidence, serta
   explicit at-least-once limitation.

## Definition of done

- user-authored Redis/provider restart dan duplicate/late callback tests direview;
- full verified/rejected E2E paths GREEN;
- durable work bertahan melewati tested outages/restarts;
- duplicate/late events tidak mengulang terminal domain effect;
- no dependency-backed test silently skips;
- full quality gate dan learning note selesai;
- Issue 009 siap ditutup dan Issue 010 menjadi urutan berikutnya.
