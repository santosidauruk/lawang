# Checkpoint 4 — Strict HTTP Path dan Runtime Wiring

Status: aktif dan siap untuk bagian pertama user. PostgreSQL-backed application
behavior sudah GREEN; MinIO adapter boleh masih berupa fake pada handler test.

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

Kerjakan bagian ini sebagai dua siklus RED -> GREEN. Jangan mengerjakan route
`upload-url` dahulu; status dan exact field names response route itu belum dibekukan.

### Siklus 1 — confirm success

1. Buka scaffold `internal/adapter/httpapi/artifact_http_test.go`. Pakai ID, waktu,
   session summary, dan fake service yang sudah disediakan agar test tetap
   deterministic.
2. Tambahkan `TestConfirmIdentityDocumentHTTPContract` di file tersebut. Buat
   `POST /verification-sessions/{sessionID}/artifacts/confirm` dengan:
   - header `Authorization: Bearer opaque-token`;
   - header `Content-Type: application/json`;
   - body `{"uploadIntentId":"<intentID>"}`.
3. Panggil route melalui public entry point `httpapi.NewHandler`, bukan handler
   function internal secara langsung. Agar test compile, perluas wiring handler
   dengan dependency confirm yang optional; semua call site lama harus tetap bisa
   mengirim `nil`.
4. Bentuk interface sekecil kebutuhan handler:

   ```go
   type ArtifactConfirmService interface {
       Confirm(context.Context, uuid.UUID, string, uuid.UUID) (session.Summary, error)
   }
   ```

   Interface ini dimiliki HTTP adapter sebagai consumer. Jangan memasukkan
   `HeadObject`, extractor, transaction, atau concrete `*artifact.Service` ke
   interface.
5. Assertion test harus membuktikan perilaku yang terlihat dari HTTP:
   - status tepat `200`;
   - `Content-Type` adalah `application/json`;
   - body tepat session summary `id`, `status`, dan `expiresAt`, tanpa field ekstra;
   - fake menerima `sessionID`, token mentah `opaque-token`, dan `uploadIntentID`
     yang benar tepat satu kali.
6. Jalankan hanya test baru. RED yang valid adalah compile failure karena dependency
   belum diterima `NewHandler`, atau `404` karena route belum terdaftar. Jangan lanjut
   jika RED berasal dari typo/setup test.
7. Tambahkan implementasi minimum: parse Bearer token, parse UUID path, decode field
   `uploadIntentId`, konversi ke `uuid.UUID`, panggil service, lalu tulis session
   summary. Daftarkan route sebagai `POST` dengan `requireMethod`.
8. Jalankan test yang sama sampai GREEN. Belum perlu menulis semua error mapping
   pada siklus ini.

### Siklus 2 — satu strict-decode behavior

1. Tambahkan satu test yang mengirim body valid ditambah field tidak dikenal, misalnya
   `{"uploadIntentId":"<intentID>","extra":"rejected"}`.
2. Assertion: response `400 VALIDATION_ERROR` dan fake service tidak dipanggil.
3. Jalankan sampai RED, lalu tambahkan `decoder.DisallowUnknownFields()` dan mapping
   validation minimum sampai kedua test GREEN.
4. Stop untuk review. `MaxBytesReader`, second JSON value, missing field, invalid
   UUID, dan error application sengaja dikerjakan sebagai siklus sibling setelah
   review; jangan mengimplementasikannya tanpa test yang lebih dulu RED.

Ini memberi pengalaman langsung menulis handler, HTTP decode, service boundary, dan
route registration tanpa sekaligus menangani seluruh matrix error.

Scaffold sengaja belum mengubah signature `NewHandler` atau mendaftarkan route.
Keputusan dan perubahan pertama itu tetap menjadi bagian concept-bearing milik user.

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
