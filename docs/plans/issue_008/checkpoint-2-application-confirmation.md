# Checkpoint 2 — Application Confirmation tanpa Document Extractor

Status: selesai pada 2026-08-13; tracer user, sibling agent, regression, dan quality
gate GREEN.

## Tujuan

Membuktikan accepted Biometric Capture melalui public `artifact.Service.Confirm`
dengan fakes kecil: metadata storage diperiksa di luar transaction, tidak ada
document extraction, lalu guarded transaction mengonfirmasi intent, membuat accepted
Verification Artifact, berpindah state, dan menulis event secara atomik.

Replay dan real concurrent coordination tetap Checkpoint 6.

## Perilaku tracer

Session berstatus `identity_document_uploaded` dengan satu pending biometric intent
dan object JPEG non-zero <= 5 MiB harus menghasilkan summary
`biometric_capture_uploaded`, satu artifact kind `biometric_capture`, dan satu
accepted `confirm_biometric_capture` event. `DocumentExtractor` call count harus nol.

## Candidate files

- `internal/application/artifact/service_test.go`
- `internal/application/artifact/service.go`
- `internal/domain/verificationsession/state.go` hanya bila tracer menemukan gap;
  state/event constants sekarang sudah tersedia.

## Hasil checkpoint

Tracer user `TestConfirmBiometricCaptureAcceptsUploadWithoutExtractionAtomically`
dipertahankan. Public `artifact.Service.Confirm` memilih state, Personal Details,
extractor, transition, dan event dari kind milik Upload Intent; caller tidak mengirim
kind kedua. Biometric Capture memakai `HeadObject` di luar transaction, tidak memuat
Personal Details, tidak memanggil `DocumentExtractor`, lalu transactionally
mengonfirmasi intent, membuat accepted Verification Artifact, berpindah ke
`biometric_capture_uploaded`, dan menulis accepted `confirm_biometric_capture` event.

Agent continuation membekukan JPEG/PNG non-zero sampai inclusive 5 MiB, penolakan
PDF/unsupported/empty/oversized, bounded storage failure, lifecycle guards, stale
external-result rejection, dan rollback saat event write gagal. Identity Document
tetap menerima JPEG/PNG/PDF sampai inclusive 10 MiB serta mempertahankan extraction
dan identity-number reconciliation.

## Bagian user — selesai dan direview

1. `[test fixture][application service]` User menyiapkan session
   `identity_document_uploaded`, pending biometric Upload Intent, valid token, fixed
   clock, memory transaction, dan fake ObjectStorage untuk satu skenario JPEG sukses.
2. `[test fixture][application service]` User mengatur fake ObjectStorage agar
   mengembalikan `image/jpeg`, ukuran non-zero di bawah 5 MiB, serta ETag non-empty
   untuk exact biometric storage key.
3. `[test fixture][application service]` User membuat DocumentExtractor fail-fast
   atau memiliki call counter yang akan menggagalkan test bila dipanggil.
4. `[unit test][application service]` User menulis test sukses pertama Biometric
   Confirm melalui public `artifact.Service.Confirm`.
5. `[unit test][application service]` User mengassert returned summary exact
   `biometric_capture_uploaded` dan `HeadObject` dipanggil untuk key milik intent.
6. `[unit test][application service]` User mengassert memory transaction menghasilkan
   confirmed intent, satu accepted biometric Verification Artifact, satu state
   transition, dan satu accepted `confirm_biometric_capture` event.
7. `[unit test][application service]` User mengassert Personal Details reconciliation
   dan DocumentExtractor masing-masing tidak dipanggil pada biometric path.
8. `[verification]` User menjalankan focused test dan mencatat RED pada behavior yang
   masih hard-coded ke identity validation, event, state, atau metadata policy.
9. `[application service]` User menulis minimal GREEN yang memilih behavior dari
   bounded intent kind: biometric memeriksa metadata tetapi melewati Personal Details
   reconciliation dan DocumentExtractor.
10. `[verification]` User menjalankan focused biometric test sampai GREEN, lalu
    menjalankan successful Identity Document confirm test sebagai regression minimum.
11. `[review]` User berhenti dan menyerahkan test, minimal GREEN, serta output
    RED/GREEN kepada agent sebelum PDF/size/empty/wrong-state cases ditambahkan.

User tetap memperbaiki kesalahan pada dispatch policy, transaction, dan no-extractor
boundary. Agent tidak mengganti tracer atau jalur inti itu diam-diam.

## Review agent

Agent memeriksa:

- kind berasal dari owned Upload Intent, bukan request tambahan;
- biometric hanya valid dari `identity_document_uploaded`;
- JPEG/PNG, non-zero, <= 5 MiB dipetakan di application code;
- PDF ditolak dan tidak mencapai transaction;
- no extractor berarti benar-benar tidak ada call, bukan dummy extraction success;
- HeadObject tetap di luar DB transaction;
- guarded re-read memvalidasi session, intent ownership/kind/key/expiry dan state;
- event exact `confirm_biometric_capture`, bukan state name;
- identity mismatch/extraction behavior tetap unchanged.

## Bagian agent — selesai

Setelah tracer user GREEN, agent mengerjakan sibling behaviors satu per satu:

1. PNG accepted;
2. zero-byte, > 5 MiB, dan PDF/unsupported content type ditolak dengan bounded
   `INVALID_OBJECT_METADATA` reasons;
3. wrong kind, wrong state, expired, superseded, not-found, dan stale re-read;
4. object-storage failure tetap safe/bounded;
5. injected event-write failure membuktikan rollback seluruh memory effects;
6. biometric path tidak memuat/merekonsiliasi Personal Details bila tidak diperlukan;
7. full Identity Document application regression, termasuk extractor dan mismatch.

## Definition of done

- user-authored success tracer direview dan GREEN;
- metadata mapping biometric dibekukan oleh tests;
- extractor call count selalu nol pada seluruh biometric outcomes;
- atomic success dan rollback terbukti;
- no raw metadata/credentials bocor ke errors/events;
- application package dan race detector GREEN.

Minimum verification:

```sh
go test ./internal/application/artifact -count=1
go test -race ./internal/application/artifact -count=1
```

Verification evidence pada 2026-08-13:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make quality
PASS: fmt-check, vet, staticcheck, full race suite, sqlc-diff,
migration-validate, and compose-validate

Full race integration packages:
ok github.com/santosidauruk/lawang-go/tests/integration
ok github.com/santosidauruk/lawang-go/tests/schema
```
