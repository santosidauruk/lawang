# Checkpoint 4 — Strict HTTP Path dan Runtime Wiring

Status: belum dimulai. PostgreSQL-backed application behavior harus GREEN terlebih
dahulu; MinIO adapter boleh masih berupa fake pada handler test.

## Tujuan

Mengekspos dua route Issue 007 melalui contract test:

- `POST /verification-sessions/{id}/artifacts/upload-url`
- `POST /verification-sessions/{id}/artifacts/confirm`

HTTP adapter memiliki decode/validation/error mapping. Application service memiliki
auth, lifecycle, dan domain behavior. `cmd/api` hanya membuat dependency dan wiring.

## File utama dan pendukung

- `internal/adapter/httpapi/router_test.go`
- `internal/adapter/httpapi/router.go` atau file artifact HTTP baru bila test
  menunjukkan pemisahan itu membuat module lebih jelas;
- `internal/adapter/httpapi/response.go`
- `cmd/api/main.go`
- `tests/integration/session_http_test.go` atau integration file Issue 007 baru.

Jangan mengubah signature `NewHandler` secara spekulatif. Test pertama menentukan
interface paling sempit yang dibutuhkan handler.

## Bagian user

1. Sebelum menulis response assertion upload URL, verifikasi keputusan status dan
   exact JSON field names dari thread. Yang sudah pasti hanya: berisi intent ID dan
   usable URL, tanpa `expiresAt`.
2. Tulis satu `httptest` untuk confirm success melalui `NewHandler`: Bearer token,
   UUID path, JSON `uploadIntentId`, service fake, exact `200` session summary.
3. Tambahkan minimal consumed interface, handler, strict decode, dan route
   registration sampai test itu GREEN.
4. Strict decode berarti `MaxBytesReader`, `DisallowUnknownFields`, tepat satu JSON
   value, required UUID string, dan explicit conversion ke `uuid.UUID`.
5. Stop untuk review sebelum menambah error cases atau upload-url route.

Ini memberi pengalaman langsung menulis handler, HTTP decode, service boundary, dan
route registration tanpa harus sekaligus menangani seluruh matrix error.

## Review agent

Agent memeriksa exact status/body/header, auth parsing, UUID validation, body limit,
unknown/trailing JSON, service tidak dipanggil pada decode failure, expected error
mapping, dan apakah handler hanya bergantung pada narrow application interface.

## Kelanjutan agent

Setelah confirm success GREEN:

1. tambah satu confirm decode/error behavior per RED -> GREEN;
2. map exact mismatch ke `422 LOCAL_VALIDATION_FAILED` dengan bounded reason;
3. map expired/superseded/auth/not-found/storage errors setelah code/message-nya
   dibekukan; unknown errors selalu safe `500 INTERNAL`;
4. tambah upload-url success contract dan handler;
5. tambah kind restriction: Issue 007 hanya menerima `identity_document`;
6. tambah malformed, unknown field, second JSON value, missing field, invalid UUID,
   wrong method, dan oversized body cases;
7. wire application/PostgreSQL dependencies di `cmd/api/main.go` dengan storage fake
   atau constructor boundary yang tersedia sampai Checkpoint 5;
8. tambah HTTP + PostgreSQL integration tracer; real MinIO tetap Checkpoint 5.

## Definition of done

- kedua route terdaftar dan method-restricted;
- decode strict dan exact public JSON contract beku dalam tests;
- expected application errors menjadi envelope aman;
- success confirm mengembalikan current session summary;
- mismatch exact `422` lulus;
- runtime wiring compile tanpa SDK type masuk application/HTTP DTO;
- existing Issue 001-006 HTTP tests tidak regress.

Command minimum:

```sh
go test ./internal/adapter/httpapi -count=1
go test ./tests/integration -run 'HTTP|Artifact' -count=1
```
