# Checkpoint 1 — Kind Policy dan Biometric Upload Intent

Status: selesai pada 2026-08-13; tracer user, sibling agent, dan regression GREEN.

## Tujuan

Membuktikan melalui public application interface bahwa `biometric_capture` dapat
membuat Upload Intent setelah Identity Document diterima, memakai TTL lima menit dan
fresh key yang tidak mungkin berbenturan dengan key Identity Document.

Checkpoint ini tidak membaca object dan tidak melakukan confirm.

## Perilaku tracer

Dengan Verification Session berstatus `identity_document_uploaded`, resume token
valid, dan clock tetap, `UploadIntentService.Create(..., "biometric_capture")`:

- presign key
  `verification-sessions/{sessionID}/biometric_capture/{intentID}`;
- menjalankan presign sebelum transaction database dibuka;
- transactionally re-read guard, supersede hanya pending biometric intent, dan insert
  fresh pending biometric intent;
- mengembalikan exact `uploadIntentId` dan `uploadUrl` tanpa mengekspos key/expiry.

## Candidate files

- `internal/application/artifact/upload_intent_service_test.go`
- `internal/application/artifact/upload_intent_service.go`
- fake transaction/presigner yang sudah ada pada package test

Jangan membuat file policy/generic abstraction sebelum RED menunjukkan seam (titik
sambungan dalam kode tempat behavior dapat divariasikan tanpa membongkar seluruh
sistem) yang benar-benar dibutuhkan.

## Hasil checkpoint

Tracer user `TestCreateBiometricUploadIntentPresignsBeforeAtomicReplacement`
dipertahankan. Minimal GREEN memilih required state secara explicit untuk kedua kind,
sementara storage-key segment memakai exact kind yang telah dibatasi sebelum session,
presigner, atau transaction disentuh. Tidak ada registry atau generic artifact
framework baru.

Agent continuation membuktikan wrong initial state, stale transactional re-read,
presign failure tanpa database effect, kind-scoped supersede yang mempertahankan
confirmed Identity Document history, repeated create dengan fresh ID/key, isolasi key
kedua kind, serta invalid-kind short-circuit. Memory transaction hanya membuktikan
application effects; PostgreSQL race invariant tetap Checkpoint 3.

Seluruh sibling test langsung GREEN ketika pertama dijalankan karena minimal GREEN
user sudah menerapkan shared flow untuk exact bounded kind. Tidak ada kegagalan RED
yang direkayasa dan tidak ada production code tambahan pada agent continuation.

## Bagian user — selesai

1. `[test fixture][application service]` Setelah agent menjelaskan fixture yang sudah
   ada, user menyiapkan Verification Session berstatus
   `identity_document_uploaded`, resume token valid, fixed clock, fake presigner, dan
   memory transaction untuk satu skenario sukses.
2. `[unit test][application service]` User menulis satu test sukses Biometric Upload
   Intent melalui public `UploadIntentService.Create` dengan exact kind
   `biometric_capture`.
3. `[unit test][application service]` User mengassert result hanya berisi fresh
   `uploadIntentId` dan `uploadUrl`, tanpa storage key atau expiry.
4. `[unit test][application service]` User mengassert presigner menerima key
   `verification-sessions/{sessionID}/biometric_capture/{intentID}` dan TTL tepat lima
   menit sebelum memory transaction dibuka.
5. `[unit test][application service]` User mengassert recorded transaction effects
   melakukan re-read guard, supersede hanya pending biometric intent, dan insert satu
   pending intent dengan exact kind/key/time.
6. `[verification]` User menjalankan focused test dan mencatat RED karena service
   masih menolak `biometric_capture` atau guard state masih identity-only.
7. `[application service]` User menulis minimal GREEN untuk memilih required state
   dan storage-key segment berdasarkan bounded kind. Pertahankan rule kedua kind
   secara explicit; jangan membuat registry atau plugin framework.
8. `[verification]` User menjalankan focused test sampai GREEN, lalu menjalankan
   existing Identity Document Upload Intent test sebagai regression minimum.
9. `[review]` User berhenti dan menyerahkan test, minimal GREEN, serta output RED/GREEN
   kepada agent sebelum failure cases atau refactor ditambahkan.

Jika review menemukan policy yang mencampur state, kind, atau storage-key rule,
user merevisinya. Agent tidak mengganti core policy diam-diam.

## Review agent

Agent memeriksa:

- identity requires `personal_details_submitted`, biometric requires
  `identity_document_uploaded`;
- key memakai exact bounded kind dan fresh application-generated intent UUID;
- presign tetap di luar transaction;
- transactional re-read membuang URL yang tidak dikembalikan bila state berubah;
- supersede dibatasi `(session, kind)` sehingga biometric tidak supersede identity;
- abstraction hanya mengumpulkan invariant yang benar-benar sama;
- Identity Document tests tetap GREEN tanpa kontrak berubah.

## Bagian agent — selesai

Setelah tracer user direview dan GREEN, agent menutup satu sibling behavior per
siklus RED -> GREEN:

1. wrong session state dan stale re-read untuk biometric;
2. presign failure tidak menulis/supersede intent;
3. existing pending biometric intent tersupersede, accepted identity history tidak
   berubah;
4. repeated create menghasilkan fresh intent/key dan hanya satu pending biometric
   effect pada fake transaction;
5. identity/biometric fixtures membuktikan kind/key isolation;
6. invalid kind tetap bounded dan tidak memanggil presigner/transaction;
7. seluruh Identity Document create tests sebagai regression gate.

Real PostgreSQL concurrency untuk one-pending-per-kind tetap Checkpoint 3; fake
application transaction di sini tidak boleh dipakai untuk mengklaim invariant
database under race.

## Definition of done

- user-authored tracer dan minimal GREEN direview;
- state, kind, key, TTL, ordering, and stale-result rules terbukti;
- tidak ada generic framework spekulatif;
- focused application tests dan race detector GREEN;
- learning note mencatat mengapa state awal berbeda per kind.

Minimum verification:

```sh
go test ./internal/application/artifact -run 'UploadIntent' -count=1
go test -race ./internal/application/artifact -run 'UploadIntent' -count=1
```

Verification evidence pada 2026-08-13:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./internal/application/artifact
PASS

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./...
PASS

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make staticcheck
PASS
```
