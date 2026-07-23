# Checkpoint 2 — Application Confirmation

Status: selesai pada 2026-07-22. Focused suite dan race detector GREEN.

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

## Audit baseline yang diselesaikan

### Temuan concept-bearing yang telah ditutup

1. Local mismatch sekarang meng-commit `validation_failed`, bounded failure code, dan
   event `local_validation_failed` secara atomik tanpa artifact/state transition.
2. Guard sesudah external I/O sekarang membandingkan ulang session, intent termasuk
   storage key/expiry, resume-token hash, serta immutable Personal Details.
3. Injected event-write failure membuktikan intent, artifact, state, dan event kembali
   ke state awal.

### Sibling behavior yang telah ditutup

- invalid token berhenti sebelum intent/details/storage/extractor dibaca;
- session expired pada `now >= expires_at`;
- intent expired, superseded, kind salah, atau not found;
- zero byte, ukuran di atas 10 MiB, dan content type di luar JPEG/PNG/PDF;
- safe bounded application errors untuk object storage/extractor failures;
- transactional re-read mendeteksi session/status/intent/details yang stale;
- test memastikan raw identity number dan raw extraction tidak masuk event/error.

Expected application failures sekarang memakai typed bounded errors agar HTTP adapter
dapat memetakannya secara stabil dan aman pada checkpoint berikutnya.

## Riwayat urutan penyelesaian

Setiap nomor adalah satu siklus RED -> GREEN. Jangan menulis semua test sekaligus.

### Bagian user sudah selesai

Tracer sukses dan minimal GREEN merupakan learning-bearing deliverable user untuk
checkpoint ini. Bila review menemukan kesalahan pada konsep transaction/re-read yang
ditulis user, user merevisinya setelah melihat failing test yang sempit.

### Kelanjutan yang telah diselesaikan

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

Hasil aktual pada 2026-07-22: kedua command minimum lulus dengan 28 test. Typed error,
file-constraint mapping, short-circuit, rollback, dan stale re-read dicatat di
`docs/learning/007-upload-intents-and-identity-validation.md`. Replay serta koordinasi
concurrent confirm tetap sengaja berada di Checkpoint 6.
