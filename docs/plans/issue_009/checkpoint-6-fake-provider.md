# Checkpoint 6 — Deterministic Fake Provider Process

Status: siap dimulai setelah Checkpoint 5 selesai pada 2026-08-28; belum aktif.

## Tujuan

Membangun separate Go HTTP process yang menerima Provider Submission, memilih
deterministic test scenario, lalu mengirim asynchronous signed Webhook Event ke
Lawang callback URL.

(`deterministic scenario` adalah konfigurasi test yang menghasilkan verdict, delay,
dan jumlah callback yang sama untuk input session yang sama.) Provider ini bukan real
identity-verification engine dan tidak boleh disebut production provider.

## Exact Provider Submission shape

Request memuat:

- `sessionId`;
- `callbackUrl`;
- immutable Personal Details;
- exactly one Identity Document dan one Biometric Capture Verification Artifact
  metadata, dengan deterministic order;
- tidak ada raw object bytes, resume token, presigned URL, atau raw extraction body.

Exact JSON field names untuk nested Personal Details/artifact metadata dibekukan
oleh user success handler test sebelum Provider Task Checkpoint 7 memakai contract
tersebut.

Test-only scenario endpoint tetap:

```http
PUT /test/scenarios/{sessionId}
Content-Type: application/json

{
  "verdict":"rejected",
  "reason":"document_invalid",
  "delayMs":50,
  "duplicateCallbacks":1
}
```

`duplicateCallbacks: 1` berarti satu callback tambahan dengan exact event ID dan
body yang sama, bukan verdict event baru.

## Candidate files

- `internal/application/fakeprovider/`
- `internal/adapter/httpapi/fake_provider.go` atau cohesive provider-owned HTTP
  package
- `internal/adapter/providerhttp/callback.go`
- `cmd/fake-provider/main.go`
- provider-specific config tests and integration tests

Fake provider code harus tetap terpisah dari Lawang applicant API composition.

## Bagian user

Agent menyiapkan callback recorder, fixed scenario store fake, HTTP test harness,
dan repetitive validation fixtures. User menulis application/HTTP/callback production
flow serta seluruh runtime wiring sesuai approval.

1. `[application service]` User mendefinisikan exact Provider Submission input dan
   scenario types tanpa memakai Lawang internal PostgreSQL/sqlc models.
2. `[http handler]` User menulis strict Provider Submission handler yang menerima
   exact approved shape dan memberikan acknowledgement tanpa menunggu callback
   selesai.
3. `[application service]` User menulis in-memory deterministic scenario selection
   dengan default verified behavior untuk manual demo.
4. `[http handler]` User menulis test-only scenario handler dengan exact verdict,
   bounded reason, non-negative delay, dan bounded duplicate count validation.
5. `[provider adapter]` User menulis callback sender yang marshal satu exact Webhook
   Event body, menghitung HMAC-SHA256 atas bytes tersebut, dan mengirim
   `x-signature: sha256=<hex>`.
6. `[application service]` User memastikan repeated Provider Submission dengan same
   `Idempotency-Key` merepresentasikan satu logical submission, bukan membuat
   uncontrolled callback sequence kedua.
7. `[verification]` User menjalankan default verified submission menuju callback
   recorder sampai GREEN.
8. `[runtime wiring]` User menambahkan typed fake-provider config untuk HTTP address,
   shutdown timeout, callback timeout, dan webhook secret dengan safe validation.
9. `[runtime wiring]` User membuat `cmd/fake-provider` composition, listener,
   graceful shutdown, safe JSON logging, dan route registration.
10. `[runtime wiring]` User menambahkan fake-provider service ke Compose/local command
    surface tanpa mencampurnya ke `cmd/api`.
11. `[verification]` User menjalankan process-level verified tracer memakai actual
    listener sampai GREEN.
12. `[review]` User berhenti sebelum delay/duplicate/failure sibling matrix.

Kesalahan signing setelah body berubah, secret/body logging, scenario leakage ke
public Lawang API, atau duplicate submission yang membuat unbounded effects adalah
concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- process benar-benar terpisah dan dapat dijalankan independen;
- provider request tidak memakai internal DB/generated types;
- exact callback bytes yang ditandatangani adalah bytes yang dikirim;
- default scenario stabil tanpa real-time randomness;
- `Idempotency-Key` validation/dedup behavior eksplisit;
- delayed callback tidak memblokir Provider Submission acknowledgement;
- runtime errors/logs tidak memuat Personal Details, artifacts, callback body, secret,
  atau URL sensitif.

## Bagian agent

Setelah user wiring direview, agent menutup:

1. all four rejected scenarios dan invalid reason combinations;
2. zero/non-zero delay menggunakan synchronization yang deterministic, bukan timing
   sleep sebagai correctness proof;
3. zero/multiple duplicate callbacks memakai exact event ID/body;
4. malformed/oversized submission dan scenario requests;
5. callback non-2xx/timeout safe error handling tanpa data leak;
6. same/different Idempotency-Key behavior dan concurrent duplicate submissions;
7. config negative matrix, health/lifecycle regression, dan process restart tests;
8. focused race suite serta Compose validation.

## Definition of done

- user-authored provider application/HTTP/callback/wiring code direview;
- fake provider berjalan sebagai separate process;
- exact approved submission shape dan signed event shape dibekukan;
- deterministic verified/rejected scenarios tersedia;
- delay/duplicate/idempotency behavior terbukti;
- tidak ada real-provider atau production-readiness claim.
