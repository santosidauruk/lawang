# Checkpoint 1 — Schema dan Invariant Upload Intent

Status: selesai dan telah direview. Panduan ini mempertahankan urutan belajar bila
checkpoint perlu dipahami oleh agent baru; jangan mengulang atau menimpa hasilnya.

## Tujuan

Membedakan Upload Intent dari object dan Verification Artifact, lalu membuktikan di
PostgreSQL bahwa paling banyak satu intent `pending` dapat ada untuk pasangan
`(verification_session_id, kind)` tanpa menghapus historical rows.

## File utama dan pendukung

- user: `sql/migrations/00005_create_upload_intents.sql`
- user: `sql/proofs/005_upload_intent_constraints.sql`
- agent continuation: `tests/integration/upload_intents_postgres_test.go`
- learning evidence: `docs/learning/007-upload-intents-and-identity-validation.md`

## Langkah user yang telah dikerjakan

1. Menulis migration forward-only dengan UUID milik aplikasi, session foreign key,
   bounded kind/status/failure, unique non-empty storage key, timestamps, dan cleanup
   invariant.
2. Menulis partial unique index untuk satu `pending` intent per session/kind.
3. Menulis disposable SQL proof untuk valid row, invalid combinations, historical
   rows, storage key uniqueness, dan replacement lifecycle.
4. Menjalankan proof di disposable PostgreSQL.

## Review yang dilakukan agent

Agent memeriksa nullability, status/failure/timestamp combinations, arti `OR` pada
check constraint, representasi historical rows, serta perilaku unique index ketika
dua transaction bersaing. Kesalahan concept-bearing dikembalikan ke user untuk
direvisi sebelum agent melanjutkan.

## Kelanjutan yang ditulis agent

Agent menambahkan test dengan dua `pgx.Conn` nyata, start signal yang sama, tanpa
timing sleep. Test membuktikan tepat satu insert commit dan satu menerima SQLSTATE
`23505` dari partial unique index, lalu final state tetap satu pending intent.

## Bukti selesai

- migration: `sql/migrations/00005_create_upload_intents.sql`
- proof: `sql/proofs/005_upload_intent_constraints.sql`
- concurrency test:
  `TestConcurrentPendingUploadIntentCreationAllowsExactlyOneWinner`
- hasil dan penjelasan PostgreSQL dicatat pada learning note.

Agent baru cukup memvalidasi bahwa file dan test masih ada serta belum regress. Jangan
mengubah migration yang sudah pernah dipakai menjadi desain baru; perubahan schema
baru harus memakai forward migration berikutnya.
