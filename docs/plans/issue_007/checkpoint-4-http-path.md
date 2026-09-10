# Checkpoint 4 — Strict HTTP Path dan Runtime Wiring

Status: selesai pada 2026-07-28. Confirm dan upload-url strict contract tests,
bounded error mapping, serta HTTP upload-url -> confirm tracer dengan PostgreSQL
disposable GREEN. Runtime artifact routes tetap sengaja disabled sampai Checkpoint 5.

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

## Bagian user — selesai

Bagian berikut merekam dua siklus RED -> GREEN yang sudah diselesaikan user. Saat
siklus ini dikerjakan, route `upload-url` sengaja belum disentuh karena status dan
exact field names-nya belum dibekukan. Keputusan tersebut sekarang sudah disetujui
dan dicatat pada bagian **Kontrak yang disetujui untuk kelanjutan agent**.

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

Scaffold awal sengaja belum mengubah signature `NewHandler` atau mendaftarkan route.
User kemudian menulis perubahan concept-bearing tersebut melalui test pertama;
agent berikutnya tidak boleh mengganti atau menulis ulang slice ini.

Hasil akhirnya berada di `internal/adapter/httpapi/artifact_http_test.go`:

- `TestConfirmIdentityDocumentHTTPContract`;
- `TestConfirmIdentityDocumentRejectsUnknownField`;
- `artifactConfirmServiceStub` sebagai fake di HTTP boundary.

Handler memakai HTTP-owned `ArtifactConfirmService`, menerima session ID dari path,
resume token dari Bearer header, dan Upload Intent ID dari body. Test success
membuktikan exact response fields dan argument service; test unknown field
membuktikan `400 VALIDATION_ERROR` serta service tidak dipanggil.

Verifikasi terakhir sebelum handoff:

```sh
GOCACHE=/tmp/lawang-build go test ./internal/adapter/httpapi \
  -run '^TestConfirmIdentityDocument(HTTPContract|RejectsUnknownField)$' \
  -count=1 -v
# 2 tests passed

GOCACHE=/tmp/lawang-build go test ./internal/adapter/httpapi -count=1
# 56 tests passed
```

## Review agent

Agent memeriksa exact status/body/header, auth parsing, UUID validation, body limit,
unknown/trailing JSON, service tidak dipanggil pada decode failure, expected error
mapping, dan apakah handler hanya bergantung pada narrow application interface.

## Kontrak yang disetujui untuk kelanjutan agent

Kontrak berikut telah disetujui user pada 2026-07-27. Jangan menanyakan ulang atau
mengganti status, code, message, field name, maupun boundary ini tanpa menemukan
kontradiksi baru dalam sumber kebenaran yang lebih tinggi.

### Success `POST /verification-sessions/{id}/artifacts/upload-url`

Response:

```http
HTTP/1.1 201 Created
Content-Type: application/json
```

```json
{
  "uploadIntentId": "21b15c5e-d8f3-4b79-ac47-d4f5fd55ac4c",
  "uploadUrl": "https://public-object-host/..."
}
```

Aturan exact:

- field hanya `uploadIntentId` dan `uploadUrl`;
- tidak ada `expiresAt`, storage key, credential, atau field internal lain;
- tidak perlu `Location` karena belum ada public GET Upload Intent route;
- `201` dipilih karena setiap success membuat Upload Intent baru, termasuk ketika
  intent pending lama disupersede.

Gunakan interface consumer-owned yang tetap sempit:

```go
type ArtifactUploadIntentService interface {
    Create(
        context.Context,
        uuid.UUID,
        string,
        string,
    ) (artifact.CreatedUploadIntent, error)
}
```

Tambahkan interface ini sebagai dependency keempat `NewHandler`. Jangan
memperlebar `ArtifactConfirmService`; confirm dan create intent adalah dua use case
berbeda. Positional constructor empat dependency diterima untuk checkpoint ini.
Refactor ke dependency/options struct ditunda sampai pertumbuhan route benar-benar
membenarkannya.

### HTTP request validation

| Perilaku | Status | Code | Exact message |
| --- | ---: | --- | --- |
| malformed, empty, unknown field, atau trailing JSON pada confirm | `400` | `VALIDATION_ERROR` | `invalid artifact confirmation request` |
| `uploadIntentId` tidak ada | `400` | `VALIDATION_ERROR` | `uploadIntentId is required` |
| `uploadIntentId` bukan UUID | `400` | `VALIDATION_ERROR` | `uploadIntentId must be a UUID` |
| session path `id` bukan UUID | `400` | `VALIDATION_ERROR` | `id must be a UUID` |
| request body melebihi 1 MiB | `413` | `PAYLOAD_TOO_LARGE` | `request body exceeds 1 MiB limit` |
| method salah | `405` | `METHOD_NOT_ALLOWED` | gunakan helper existing dan exact `Allow` header |

Validation failure harus berhenti di HTTP boundary: service call count tetap nol.
Gunakan `MaxBytesReader`, `DisallowUnknownFields`, tepat satu JSON value, pointer
untuk required presence, lalu explicit `uuid.Parse`.

Untuk upload-url, gunakan pola validation yang sama dengan message
`invalid artifact upload URL request`; missing/invalid `kind` dipetakan melalui
contract kind di bawah.

### Bounded application error mapping

Session errors tetap memakai mapping existing:

| Error | Status |
| --- | ---: |
| `INVALID_RESUME_TOKEN` | `401` |
| `SESSION_NOT_FOUND` | `404` |
| `SESSION_EXPIRED` | `410` |

Artifact mapping yang telah disetujui:

| Application code/kasus | Status | Public code | Exact message | Details |
| --- | ---: | --- | --- | --- |
| local identity mismatch | `422` | `LOCAL_VALIDATION_FAILED` | `The uploaded identity document did not match the submitted details` | `{"reason":"identity_number_mismatch"}` |
| `UPLOAD_INTENT_NOT_FOUND` | `404` | same as application | `upload intent not found` | none |
| `UPLOAD_INTENT_EXPIRED` | `409` | same as application | `upload intent expired` | none |
| `UPLOAD_INTENT_SUPERSEDED` | `409` | same as application | `upload intent was superseded` | none |
| `INVALID_UPLOAD_INTENT_KIND` saat confirm | `409` | same as application | `upload intent kind is not valid for identity document confirmation` | none |
| kind selain `identity_document` pada upload-url | `400` | `INVALID_UPLOAD_INTENT_KIND` | `only identity_document uploads are supported` | none |
| `INVALID_OBJECT_METADATA` | `422` | same as application | `uploaded object metadata is invalid` | bounded application `reason` only |
| `CONFIRMATION_STALE` | `409` | same as application | `artifact confirmation state changed; retry the request` | none |
| `UPLOAD_INTENT_STALE` | `409` | same as application | `upload intent state changed; retry the request` | none |
| `OBJECT_STORAGE_FAILED` | `503` | same as application | `object storage is temporarily unavailable` | none |
| `DOCUMENT_EXTRACTION_FAILED` | `500` | same as application | `identity document processing failed` | none |
| error tidak dikenal | `500` | `INTERNAL` | `internal server error` | none |

Jangan mengekspos SDK/SQL error, storage key, URL internal, resume token, identity
number, object body, atau extraction output. Hanya mismatch dan invalid object
metadata yang memiliki bounded `details.reason`.

### Deterministic fake dan runtime boundary

HTTP + PostgreSQL integration test memakai fake object storage dan fake extractor.
Fake extractor dikontrol secara eksplisit berdasarkan storage key, bukan filename
parsing atau object metadata yang memuat identity number:

```go
type fakeDocumentExtractor struct {
    results map[string]artifact.DocumentExtraction
    errors  map[string]error
}
```

Ini membuat success, mismatch, dan extraction failure repeatable tanpa menyimpan raw
OCR output. Missing key harus menjadi explicit test failure atau configured error;
jangan diam-diam mengembalikan zero value.

Pada Checkpoint 4, `cmd/api` tetap mengirim dependency artifact `nil` sehingga route
artifact belum aktif pada production runtime. Handler routes tetap diuji melalui
`httptest`, sedangkan public path sampai PostgreSQL diuji dengan fake storage dan
extractor. Checkpoint 5 mengganti boundary ini dengan MinIO/public-host presign dan
baru mengaktifkan runtime artifact routes. Jangan membuat temporary production fake
yang menerima upload tanpa real `HeadObject`.

## Kelanjutan agent — executable handoff

Bagian user sudah selesai. Agent berikutnya harus mempertahankan kedua test tersebut
dan mengerjakan urutan berikut satu test -> minimal implementation -> GREEN pada satu
waktu. Jangan menulis seluruh matrix test dahulu.

1. **Baseline dan cleanup kecil**
   - Jalankan kedua focused user tests dan full `httpapi`.
   - Pastikan diagnostic unknown-field mengharapkan `http.StatusBadRequest` dan
     menyertakan response body. Per 2026-07-27 cleanup ini sudah terlihat di checkout;
     re-check, jangan mengulang bila sudah benar.
   - Jangan revert perubahan user atau perubahan PostgreSQL/application yang sudah ada.
2. **Confirm required field**
   - RED: body `{}` menghasilkan exact missing-field envelope dan zero service calls.
   - GREEN: return segera setelah missing-field response.
3. **Confirm invalid UUID**
   - RED: `uploadIntentId:"not-a-uuid"` menghasilkan exact UUID envelope dan zero
     service calls.
   - GREEN: parse string melalui `uuid.Parse`; jangan decode langsung ke `uuid.UUID`.
4. **Confirm JSON framing**
   - Satu siklus per malformed, empty, dan second JSON value.
   - Semua menjadi exact `400 VALIDATION_ERROR`, bukan legacy `500`.
5. **Confirm body limit**
   - RED: body di atas 1 MiB menghasilkan exact `413 PAYLOAD_TOO_LARGE`, zero service
     calls.
   - GREEN: kenali `*http.MaxBytesError` secara eksplisit.
6. **Confirm auth/path/method**
   - Tambah missing/malformed Bearer, invalid path UUID, dan wrong-method cases satu
     per satu. Reuse helper existing dan assert `Allow`.
7. **Confirm mismatch**
   - Fake service mengembalikan `artifact.Error{LOCAL_VALIDATION_FAILED,
     identity_number_mismatch}`.
   - Bekukan exact `422` body dari tabel di atas dan pastikan response tidak
     mengandung identity number atau raw extraction.
8. **Confirm bounded application errors**
   - Buat `writeArtifactError` atau helper ekuivalen di `response.go`.
   - Tambah satu test per row mapping; session errors harus diteruskan ke
     `writeSessionError`, unknown error tetap safe `500 INTERNAL`.
9. **Upload-url success**
   - RED: public test melalui `NewHandler` membuktikan auth, session ID, kind,
     service arguments, exact `201`, exact two-field body, dan tanpa `expiresAt`.
   - GREEN: tambah narrow interface keempat, DTO, handler, dan POST route minimum.
   - Perbarui semua call site lama dengan dependency keempat `nil`; jangan isi
     dependency confirm/upload dengan concrete type di HTTP adapter.
10. **Upload-url validation dan errors**
    - Satu siklus per unknown/malformed/trailing/missing kind, kind selain
      `identity_document`, oversized body, auth/path/method, session errors,
      `UPLOAD_INTENT_STALE`, dan `OBJECT_STORAGE_FAILED`.
11. **HTTP + PostgreSQL integration tracer**
    - Gunakan PostgreSQL disposable dan adapter dari Checkpoint 3.
    - Construct real `artifact.Service` dan `artifact.UploadIntentService`.
    - Object storage/presigner/extractor tetap explicit fakes; extractor memakai
      storage-key result map yang disetujui.
    - Minimal tracer menjalankan HTTP upload-url, mengambil returned
      `uploadIntentId`, lalu HTTP confirm dan membuktikan current session summary.
      Verifikasi database outcome boleh memakai existing Checkpoint 3 proof sebagai
      pendukung; jangan mengganti public HTTP assertion dengan query-only test.
12. **Compile runtime boundary dan regression**
    - `cmd/api` tetap compile dengan artifact dependencies `nil` sampai Checkpoint 5.
    - Jalankan existing Issue 001-006 HTTP suite, focused integration, race test yang
      relevan, vet, formatting, dan diff check.
13. **Documentation closeout**
    - Update status checkpoint hanya setelah semua Definition of Done Checkpoint 4
      yang tidak memerlukan real MinIO terpenuhi.
    - Catat dengan eksplisit bahwa usable public-host URL dan real `HeadObject`
      masih belum terbukti dan tetap menjadi Checkpoint 5.
    - Jangan menarik replay/concurrent-confirm/advisory lock dari Checkpoint 6.

## Definition of done

- kedua route terdaftar secara conditional pada handler dan method-restricted;
- decode strict dan exact public JSON contract beku dalam tests;
- expected application errors menjadi envelope aman;
- success confirm mengembalikan current session summary;
- mismatch exact `422` lulus;
- upload-url success mengembalikan exact `201` two-field body;
- HTTP + PostgreSQL tracer membuktikan upload-url -> confirm melalui public handler;
- `cmd/api` compile dengan artifact routes sengaja disabled sampai Checkpoint 5;
- tidak ada SDK type masuk application/HTTP DTO;
- existing Issue 001-006 HTTP tests tidak regress.

Command minimum:

```sh
go test ./internal/adapter/httpapi -count=1
go test ./tests/integration -run 'HTTP|Artifact' -count=1
```

## Hasil closeout

Checkpoint 4 selesai dengan bukti berikut:

- `internal/adapter/httpapi/artifact_http_test.go` mempertahankan dua test bagian
  user dan menambahkan sibling confirm validation/error contracts;
- `internal/adapter/httpapi/artifact_upload_http_test.go` membekukan strict decode,
  1 MiB limit, auth/path/method, kind, session, stale, storage, dan safe unknown-error
  contracts untuk upload-url;
- `tests/integration/artifact_http_postgres_test.go` menjalankan public HTTP
  upload-url, memakai returned `uploadIntentId` untuk public HTTP confirm, dan
  membuktikan current session summary melalui real application services serta
  PostgreSQL adapter;
- presigner, object metadata, dan extractor tetap fake eksplisit. Fake object dan
  extractor gagal bila storage key belum dikonfigurasi; tidak ada fallback zero value;
- `cmd/api` compile dengan kedua artifact dependency `nil`.

Verifikasi 2026-07-28:

```text
GOCACHE=/tmp/lawang-build go test ./internal/adapter/httpapi ./cmd/api -count=1
98 tests passed

GOCACHE=/tmp/lawang-build go test -race ./internal/adapter/httpapi -count=1
98 tests passed

GOCACHE=/tmp/lawang-build go test ./tests/integration -run 'HTTP|Artifact' -count=1
7 tests passed

GOCACHE=/tmp/lawang-build go test -race ./tests/integration \
  -run '^TestIdentityDocumentUploadAndConfirmOverHTTPWithPostgreSQL$' -count=1
1 test passed

GOCACHE=/tmp/lawang-build go vet \
  ./internal/adapter/httpapi ./cmd/api ./tests/integration
No issues found
```

Checkpoint ini tidak membuktikan bahwa URL response benar-benar usable dari public
host dan tidak memakai real MinIO `HeadObject`. Kedua bukti tersebut tetap menjadi
syarat Checkpoint 5. Replay/concurrent-confirm tetap menjadi Checkpoint 6.
