# Checkpoint 6 — Replay, Concurrency, dan Identity Regression

Status: approved; menunggu Checkpoint 5 selesai.

## Tujuan

Menutup idempotency dan race matrix untuk Biometric Capture dengan coordinator
(pengatur satu rangkaian confirm agar lock dan dependency dipakai pada urutan yang
aman) PostgreSQL yang sudah dibuktikan di Issue 007, lalu memastikan generalization
Issue 008 tidak merusak Identity Document behavior.

Tidak ada lock strategy baru yang dipilih di sini kecuali RED membuktikan existing
Upload Intent advisory-lock scope tidak cukup.

## Perilaku tracer

Dua `Confirm` concurrent untuk biometric Upload Intent yang sama:

- keduanya mengembalikan recorded success yang sama;
- `HeadObject` dijalankan tepat satu kali total;
- `DocumentExtractor` dijalankan nol kali;
- database memiliki satu accepted biometric Verification Artifact, satu
  `confirm_biometric_capture` event, dan final state
  `biometric_capture_uploaded`;
- readiness predicate true dan tidak memiliki duplicate effects.

## Candidate files

- `tests/integration/artifact_confirm_concurrency_test.go`
- application/postgres coordinator tests existing sebagai regression surface;
- `docs/learning/008-kind-specific-biometric-upload.md` pada completion.

## Bagian user

1. `[test fixture][postgres adapter]` User membuka disposable PostgreSQL dengan pool
   minimal dua connections dan mendaftarkan pool/container cleanup.
2. `[test fixture][postgres adapter]` User seed session
   `identity_document_uploaded`, accepted Identity Document Verification Artifact,
   dan satu pending biometric Upload Intent dengan ownership/key yang valid.
3. `[test fixture][application service]` User merakit public
   `artifact.Service.Confirm` memakai real PostgreSQL adapter, existing confirm
   coordinator, fixed clock, production token issuer, pointer ObjectStorage fake,
   dan fail-fast extractor.
4. `[test fixture][application service]` User menambahkan rendezvous (titik temu
   sinkronisasi agar dua goroutine benar-benar bertabrakan pada bagian yang diuji),
   synchronized HeadObject call counter, result channels, dan bounded timeout.
5. `[integration test][application service]` User menulis satu concurrent-confirm
   tracer yang memulai dua goroutine secara terkoordinasi dan memanggil public
   `Confirm` dengan session/intent/token yang sama.
6. `[integration test][postgres adapter]` User mengassert kedua caller menerima
   recorded summary `biometric_capture_uploaded` tanpa memanggil advisory-lock query
   secara langsung dari test.
7. `[integration test][application service]` User mengassert HeadObject dipanggil
   tepat satu kali total dan DocumentExtractor dipanggil nol kali.
8. `[integration test][postgres adapter]` User mengassert durable effects tepat satu
   biometric Verification Artifact, satu `confirm_biometric_capture` event, one
   confirmed intent, final state biometric, dan readiness `true`.
9. `[verification]` User menjalankan focused tracer dengan race detector dan mencatat
   apakah hasilnya RED atau langsung GREEN. Direct GREEN membuktikan coordinator
   existing reusable dan bukan alasan membuat coordinator kedua.
10. `[postgres adapter]` Hanya jika behavioral RED membuktikan gap koordinasi nyata,
    user menulis perubahan minimum pada existing coordinator/adapter; jangan mendesain
    interface baru hanya dari dugaan.
11. `[verification]` User menjalankan tracer sampai GREEN dengan race detector dan
    memastikan tidak ada timeout, goroutine leak, atau connection leak.
12. `[review]` User berhenti dan menyerahkan tracer, perubahan minimum, serta output
    RED/GREEN kepada agent sebelum replay/waiter/supersede matrix ditambahkan.

Kesalahan sinkronisasi, shared raw connection, lock lifetime, atau assertion yang
tidak membuktikan concurrency dikembalikan kepada user untuk direvisi.

## Review agent

Agent memeriksa:

- session-level advisory lock tetap scoped oleh Upload Intent ID;
- satu pinned connection tidak dipakai concurrent oleh dua goroutine;
- test memiliki deterministic rendezvous, timeout, cleanup, dan no goroutine leak;
- lock mencakup external HeadObject tetapi tidak membuka DB transaction selama I/O;
- replay membaca recorded outcome tanpa external work baru;
- kind/event/state tidak hard-coded identity di coordinator;
- direct biometric readiness evidence tetap berasal dari accepted rows.

## Bagian agent

Setelah tracer user benar, agent menutup satu behavior per RED -> GREEN:

1. sequential confirmed replay tanpa HeadObject/event/artifact tambahan;
2. second concurrent caller benar-benar menunggu lock dan returns recorded success;
3. waiting caller cancellation tidak merilis lock milik first caller atau merusak
   outcome;
4. supersede/expiry/create-vs-confirm races membuang external result dan tidak membuat
   partial effects;
5. concurrent create untuk kedua kinds menjaga one-pending-per-kind dan key isolation;
6. full Identity Document replay/mismatch/extractor/concurrency regression;
7. focused race suite, full quality gate, dan learning note evidence.

## Definition of done

- user-authored concurrent tracer direview;
- replay/concurrency menghasilkan satu external/storage execution dan satu durable
  outcome;
- cancellation dan stale races tidak bocor lock/connection/goroutine;
- no extractor pada biometric dan existing extractor semantics pada identity;
- readiness tetap artifact-derived under races;
- focused race tests dan full project quality gate GREEN;
- `docs/learning/008-kind-specific-biometric-upload.md` merekam decisions dan actual
  verification commands.

Command completion harus memakai test names aktual. Full closeout juga menjalankan
quality gate proyek dan memastikan tidak ada Docker integration test yang skip
diam-diam.
