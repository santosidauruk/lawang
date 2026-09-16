# Checkpoint 6 — Readiness dan Safe Observability

Status: menunggu Checkpoint 5 selesai dan gate `[review]` lulus.

## Tujuan

Menambahkan bounded `/health/ready`, mempertahankan `/health/live` tanpa dependency
I/O, dan melengkapi request/task correlation tanpa membocorkan sensitive data.

API readiness memeriksa PostgreSQL dan MinIO bucket karena keduanya adalah dependency
langsung applicant API. Redis dan verification provider tidak diperiksa oleh API
readiness karena keduanya hanya dipakai worker asynchronous path.

## Istilah baru

- `bounded timeout` adalah batas waktu eksplisit yang menghentikan dependency check
  agar health request tidak menunggu tanpa batas.
- `non-mutating check` adalah pemeriksaan dependency yang tidak membuat bucket, row,
  task, atau external side effect.
- `dependency fan-out` adalah menjalankan beberapa dependency checks dari satu
  readiness operation dengan satu bounded completion policy.
- `log cardinality` adalah jumlah variasi nilai field log yang perlu dikendalikan
  agar log tetap operasional dan tidak berubah menjadi payload dump.

## HTTP contract

Liveness tetap exact:

```http
GET /health/live

HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ok"}
```

Readiness success:

```http
GET /health/ready

HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

Readiness failure:

```http
GET /health/ready

HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{"status":"not_ready"}
```

Failure response tidak menyebut dependency name, address, SDK error, atau credential.

## Candidate files

- `internal/application/health/service.go`
- `internal/adapter/postgres/health.go`
- `internal/adapter/s3storage/health.go`
- `internal/adapter/httpapi/router.go`
- `internal/adapter/httpapi/request_logging.go`
- `internal/adapter/queue/server.go`
- `cmd/api/main.go`
- focused health, logging, and process tests

## Bagian user

Agent lebih dulu menyiapkan dependency recorders, timeout fixture, HTTP RED contract,
dan sensitive-log fixtures; user tidak menulis repetitive failure matrix.

1. `[application service]` Definisikan dua port kecil: satu untuk memeriksa
   PostgreSQL dan satu untuk memeriksa object storage. Masing-masing port cukup
   menerima context dan mengembalikan availability/error yang aman; jangan membawa
   `pgx`, AWS SDK, address, atau credential types ke application package.
2. `[application service]` Tulis readiness operation yang membuat satu bounded
   context untuk seluruh pemeriksaan. Jalankan kedua ports sesuai completion policy
   yang dipilih, batalkan sisa kerja ketika context selesai, dan kembalikan hanya
   hasil `ready` bila semua dependency wajib berhasil; hasil lain menjadi
   `not_ready`.
3. `[postgres adapter]` Implementasikan port PostgreSQL dengan pool ping yang menerima
   context dari application service. Jangan menjalankan domain query, migration,
   insert, atau update; map driver error menjadi availability failure tanpa
   mengekspos DSN maupun raw database error.
4. `[object storage adapter]` Implementasikan port MinIO dengan pemeriksaan bucket
   yang hanya membaca keberadaan/akses bucket. Jangan memakai `EnsureBucket` atau
   operation lain yang dapat membuat resource; map SDK/network failure ke safe
   availability failure.
5. `[http handler]` Daftarkan `GET /health/ready` dan panggil readiness service dari
   handler. Tulis exact `200 {"status":"ready"}` untuk success dan
   `503 {"status":"not_ready"}` untuk failure; route tidak boleh menyebut dependency
   mana yang gagal.
6. `[observability]` Di request middleware, terima request ID hanya bila format dan
   panjangnya aman; bila tidak ada atau tidak valid, buat ID baru. Simpan ID final ke
   request context dan response header agar application/adapter log dapat memakai
   nilai yang sama.
7. `[observability]` Saat request selesai, log hanya route pattern, method, status,
   duration, safe error code, dan request ID. Gunakan registered route pattern alih-
   alih raw URL/path agar UUID atau query string applicant tidak menjadi log field.
8. `[observability]` Untuk task completion, gunakan TaskID, bounded task type,
   attempt, duration, outcome, dan safe session ID sebagai correlation fields.
   Jangan log task payload, callback URL, storage key, credentials, raw error, atau
   applicant data.
9. `[runtime wiring]` Tambahkan readiness timeout ke typed API config, parse sebagai
   positive duration, dan berikan default lokal yang terlihat. Di `cmd/api`, buat
   PostgreSQL/object-storage health adapters dan readiness service, lalu berikan
   service tersebut ke HTTP handler.
10. `[runtime wiring]` Pertahankan `/health/live` sebagai handler yang berdiri sendiri
    dan tidak menerima readiness service atau dependency client. Saat menyusun router,
    pastikan route liveness tetap dapat merespons walaupun PostgreSQL atau MinIO
    sedang unavailable.
11. `[verification]` Jalankan health handler success test lebih dulu, kemudian each-
    dependency failure, timeout, cancellation, request-log, dan task-log tracers.
    Periksa exact body/status, dependency call count, shared request ID, serta raw JSON
    logs untuk memastikan tidak ada sensitive field.
12. `[review]` Berhenti setelah focused health dan logging evidence GREEN. Serahkan
    application ports/service, adapters, handler, runtime wiring, exact HTTP samples,
    dependency call counts, dan safe log samples kepada agent sebelum sibling matrix
    dilanjutkan.

Kesalahan readiness dependency scope, mutating check, unbounded timeout, atau log
leakage adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- liveness memiliki zero dependency calls;
- readiness hanya memeriksa PostgreSQL dan MinIO bucket;
- timeout/cancellation mencapai kedua adapters;
- no dependency-specific detail keluar lewat body atau log;
- request ID response header dan context menggunakan nilai yang sama;
- incoming malformed/oversized request ID tidak menyebabkan unbounded log field;
- task logging tidak mengubah provider/expiry/cleanup retry behavior.

## Bagian agent

Setelah gate `[review]` lulus, agent menambahkan:

1. readiness success, each-dependency failure, timeout, cancellation, dan method
   contract matrix;
2. proof bahwa liveness tetap `200` ketika PostgreSQL/MinIO unavailable dan zero I/O;
3. request-ID generation/propagation, safe replacement untuk invalid input, route
   pattern, error code, dan response-header regressions;
4. provider/expiry/cleanup task correlation serta secret/payload/raw-error exclusion;
5. concurrent readiness and shutdown race tests;
6. config negative matrix, focused race detector, vet, Compose validation, dan
   `git diff --check`.

## Stop point

Checkpoint selesai ketika exact health contract, bounded dependency checks, and safe
correlation logs GREEN. OpenAPI publication tetap milik Checkpoint 7.
