# Checkpoint 2 — Exact-Raw-Body Webhook HMAC Foundation

Status: selesai pada 2026-08-25; tracer user direview dan seluruh sibling/security
regression Bagian agent GREEN.

## Tujuan

Membuktikan bahwa webhook hanya meneruskan body setelah HMAC-SHA256 atas exact raw
bytes valid. (`HMAC` adalah tanda autentikasi berbasis shared secret yang membuktikan
body tidak diubah dan dikirim pihak yang mengetahui secret.) (`raw bytes` adalah byte
request persis sebelum JSON di-decode atau dibentuk ulang.)

Checkpoint ini belum membuat `webhook_events`, belum mengubah Verification Session,
dan meneruskan verified body hanya ke application-owned stub.

## Perilaku tracer

Untuk exact JSON bytes dan header
`x-signature: sha256=<lowercase-or-uppercase-hex>`:

- signature dihitung dengan HMAC-SHA256 memakai configured provider secret;
- comparison memakai `hmac.Equal`;
- valid signature meneruskan exact bytes satu kali ke service;
- satu perubahan whitespace pada body membuat signature lama invalid;
- invalid signature menghasilkan `401` sebelum JSON decode, logging, atau
  persistence.

## Candidate files

- `internal/adapter/httpapi/webhook.go`
- `internal/adapter/httpapi/webhook_test.go`
- `internal/application/providerverdict/` untuk consumer-owned verified-body port
- `internal/platform/config/config.go`

Jangan meletakkan HMAC di provider-verdict domain service; raw request bytes dan
header adalah tanggung jawab HTTP adapter.

## Bagian user — selesai dan direview

Agent menyiapkan test scaffold dengan fixed secret, exact body bytes, dan recording
service tanpa mengisi assertion concept-bearing atau verifier production.

1. `[unit test][http handler]` User menulis success test yang menghitung signature
   dari exact JSON bytes lalu memanggil public handler route
   `POST /webhooks/verification`.
2. `[unit test][http handler]` User mengassert valid request diteruskan ke service
   tepat satu kali dengan byte slice yang sama persis.
3. `[unit test][http handler]` User menulis satu test yang mengubah whitespace body
   tetapi mempertahankan signature lama dan mengassert `401` tanpa service call.
4. `[verification]` User menjalankan kedua test dan mencatat RED karena verifier atau
   route belum ada.
5. `[http handler]` User menulis signature parser minimum untuk exact prefix
   `sha256=` dan hex bytes tanpa mendecode JSON terlebih dulu.
6. `[http handler]` User menulis HMAC-SHA256 verifier minimum dan membandingkan
   expected/received bytes dengan `hmac.Equal`.
7. `[http handler]` User menulis minimal webhook handler yang membatasi pembacaan
   body, memverifikasi signature, lalu meneruskan exact verified bytes ke service.
8. `[verification]` User menjalankan focused tests sampai GREEN dan memeriksa bahwa
   recorder menerima original bytes, bukan re-marshaled JSON.
9. `[review]` User berhenti dan menyerahkan test, verifier, handler, serta output
   RED/GREEN sebelum malformed/security matrix ditambahkan.

Kesalahan verify-after-decode, ordinary string comparison, atau body logging adalah
concept-bearing dan dikembalikan kepada user untuk direvisi.

## Review agent

Agent memeriksa:

- body dibaca sekali dengan hard limit;
- signature diverifikasi sebelum decode, service call, persistence, atau logging;
- parser membedakan malformed encoding dari valid hex;
- `hmac.Equal` menerima equal-length decoded MAC bytes;
- secret tidak tampil dalam error/log/test output;
- valid service stub tidak diartikan sebagai verdict persistence.

## Bagian agent — selesai

Setelah review lulus, agent menutup:

1. missing header, wrong scheme, empty/malformed/odd-length hex, serta invalid MAC;
2. lowercase dan uppercase hex success;
3. oversized body dengan safe public error;
4. read failure dan service failure tanpa body/secret leakage;
5. valid signature dengan malformed JSON tetap belum di-decode pada security gate;
   strict event decode menjadi Checkpoint 5;
6. request-logging regression membuktikan webhook body/signature tidak pernah masuk
   log;
7. focused HTTP race tests dan existing applicant-route regressions.

## Definition of done

- required user-authored exact-raw-body HMAC test direview;
- user-authored production verifier memakai `hmac.Equal`;
- invalid signatures menghasilkan `401` tanpa downstream effect;
- no webhook table, verdict transition, provider process, atau queue dependency;
- Checkpoint 1 dan 2 learning gates keduanya GREEN sebelum continuation besar agent.

## Completion evidence

Tracer user `TestWebhookForwardExactRawBodyWithValidSignature` dan
`TestWebhookForwardWrongRawBodyWithValidSignature` GREEN. Tracer menghitung HMAC
secara independen dari helper produksi, melewati public route, membuktikan exact raw
bytes diteruskan satu kali, dan mempertahankan signature lama ketika satu trailing
space ditambahkan.

Bagian agent menambahkan `webhook_agent_test.go` untuk:

- missing header, wrong scheme, empty/malformed/odd-length hex, serta invalid MAC
  menjadi exact safe `401 INVALID_SIGNATURE` tanpa service call;
- uppercase hex success; lowercase success tetap dibuktikan tracer user;
- body di atas 1 MiB menjadi bounded `413 PAYLOAD_TOO_LARGE`;
- read failure dan verified-body service failure menjadi safe `500 INTERNAL` tanpa
  raw error, body, atau secret leakage;
- valid signature atas malformed JSON diteruskan tanpa JSON decode;
- request log hanya menyimpan bounded HTTP outcome dan tidak memuat webhook body,
  signature, header name, atau configured secret;
- required provider webhook secret dibaca dan divalidasi sekali oleh config lalu
  diinjeksi dari `cmd/api`; `.env.example` hanya memuat safe local placeholder.

Verification GREEN:

- focused user dan agent webhook tests;
- `go test -race ./internal/adapter/httpapi -count=1`;
- `go test ./internal/platform/config -count=1`;
- scoped staticcheck untuk HTTP, config, provider-verdict stub, dan API runtime;
- `go vet ./...`, `make sqlc-diff`, `make migration-validate`,
  `make compose-validate`, dan `git diff --check`.

Repository-wide `make quality` belum GREEN karena pekerjaan Checkpoint 1 yang sudah
ada memiliki duplicate SQLC import dan dua unused fixture helpers. Full
`go test -race ./...` melewati seluruh package non-integration tetapi integration
fixtures Issue 008 yang hanya memasang migration 00001-00006 gagal setelah generated
session query Checkpoint 1 mulai membaca `verification_deadline_at` dari migration
00007. Ini dicatat sebagai Checkpoint 1 continuation/regression work; tidak ada
failure pada focused webhook atau applicant HTTP adapter suite Checkpoint 2.
