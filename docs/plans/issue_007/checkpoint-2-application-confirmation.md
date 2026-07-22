# Checkpoint 2 — Application Confirmation

Status: aktif. Jalur sukses pertama sudah GREEN, tetapi checkpoint belum selesai.

## Tujuan

Membuktikan perilaku `artifact.Service.Confirm` melalui public application interface
dengan fakes kecil. External I/O dilakukan sebelum transaction; transaction kemudian
membaca ulang guard dan meng-commit outcome sukses atau Local Validation Failure
secara atomik.

Replay dan koordinasi concurrent confirm tidak diselesaikan di sini; keduanya ada di
Checkpoint 6.

## File utama dan pendukung

- user-authored test dan memory transaction:
  `internal/application/artifact/service_test.go`
- user-authored minimal GREEN:
  `internal/application/artifact/service.go`
- bounded event metadata:
  `internal/domain/sessionevent/event.go`
- referensi pola aplikasi:
  `internal/application/personaldetails/service.go`

## Yang sudah dikerjakan user

1. Menulis `TestConfirmIdentityDocumentAcceptsValidatedUploadAtomically`.
2. Membuat memory transactor dengan copy-on-commit sehingga callback error tidak
   mempublikasikan partial state.
3. Membuat fake ObjectStorage, extractor, token issuer, dan clock.
4. Menulis minimal `Confirm` yang memeriksa object metadata dan extraction di luar
   transaction, lalu melakukan guarded write di dalam transaction.
5. Membuktikan summary, confirmed intent, satu Verification Artifact, state
   transition, dan satu accepted event pada test sukses.

Focused test saat audit dokumen ini adalah GREEN.

## Audit: yang masih kurang

### Sisa concept-bearing

1. **Local mismatch belum atomik.** Implementasi sekarang mengembalikan
   `errors.New("identity number mismatch")` sebelum transaction. Kontrak meminta
   intent menjadi `validation_failed`, `failure_code=identity_number_mismatch`, satu
   event `confirm_identity_document` ber-outcome `local_validation_failed`, tanpa
   artifact dan tanpa perubahan session state.
2. **Guard sesudah external I/O belum lengkap.** Metadata/extraction diperoleh untuk
   `storedIntent.StorageKey`, tetapi hasil itu dapat dipakai bersama
   `lockedIntent.StorageKey` tanpa membuktikan keduanya sama. Semua state yang menjadi
   dasar external I/O harus dibandingkan setelah lock; hasil stale harus dibuang.
3. **Rollback pada failure belum dibuktikan.** Test sukses membuktikan commit sebagai
   satu unit, tetapi belum ada injected write failure yang membuktikan intent,
   artifact, state, dan event semuanya kembali ke state awal.

### Sibling behavior yang belum ada

- invalid token berhenti sebelum intent/details/storage/extractor dibaca;
- session expired pada `now >= expires_at`;
- intent expired, superseded, kind salah, atau not found;
- zero byte, ukuran di atas 10 MiB, dan content type di luar JPEG/PNG/PDF;
- safe bounded application errors untuk object storage/extractor failures;
- transactional re-read mendeteksi session/status/intent/details yang stale;
- test memastikan raw identity number dan raw extraction tidak masuk event/error.

`errors.New` yang tersebar belum cukup sebagai kontrak aplikasi karena HTTP adapter
tidak dapat memetakan expected failures secara stabil dan aman.

## Urutan penyelesaian

Setiap nomor adalah satu siklus RED -> GREEN. Jangan menulis semua test sekaligus.

### Bagian user sudah selesai

Tracer sukses dan minimal GREEN merupakan learning-bearing deliverable user untuk
checkpoint ini. Bila review menemukan kesalahan pada konsep transaction/re-read yang
ditulis user, user merevisinya setelah melihat failing test yang sempit.

### Kelanjutan agent setelah user meminta implementasi

1. Tambahkan satu test mismatch atomik. Pastikan RED karena state masih `pending`.
   Minta user memperbaiki jalur mismatch karena ini bagian inti transaction yang ia
   tulis; review sampai GREEN.
2. Tambahkan satu test stale storage key. Pastikan hasil HeadObject/extractor tidak
   boleh dicatat untuk key berbeda; user memperbaiki guarded re-read sampai GREEN.
3. Agent menambahkan typed/bounded artifact errors dan sibling tests satu per satu
   untuk intent expiry/supersession/kind serta object validation.
4. Agent menambahkan invalid-token short-circuit test dan minimal behavior.
5. Agent menambahkan injected failure pada memory transaction untuk membuktikan
   rollback semua database-like effects.
6. Agent menambahkan stale session/intent/detail re-read cases yang belum tercakup.
7. Setelah seluruh application tests GREEN, refactor duplikasi guard hanya bila test
   tetap GREEN setelah setiap perubahan.

Untuk mismatch, test harus mengamati committed application state, bukan jumlah call
private helper. Untuk short-circuit external boundary, call counter pada fake boleh
digunakan karena “tidak mengulang/tidak memanggil external work” adalah behavior yang
memang menjadi kontrak.

## Definition of done

- success dan mismatch outcome atomik;
- guarded re-read membuang external result yang stale;
- expected failures typed dan tidak membocorkan data;
- file constraint mapping dibekukan oleh tests;
- rollback terbukti;
- focused application package lulus dengan race detector;
- learning note diperbarui dengan alasan external I/O berada di luar transaction.

Command minimum:

```sh
go test ./internal/application/artifact -count=1
go test -race ./internal/application/artifact -count=1
```

Jika Go build cache tidak writable di environment agent, arahkan `GOCACHE` ke
`/tmp/lawang-go-build`; itu masalah environment, bukan kegagalan test produk.
