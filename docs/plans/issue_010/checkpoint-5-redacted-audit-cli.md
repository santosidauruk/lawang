# Checkpoint 5 — Redacted Audit CLI

Status: menunggu Checkpoint 4 selesai dan gate `[review]` lulus.

## Tujuan

Menyediakan `cmd/audit --session-id <uuid>` untuk authorized local/operational use
yang mencetak current session state dan ordered Session Event history tanpa token,
PII, URL, raw payload, object content, atau internal error.

Audit tetap local CLI karena Issue 010 belum memiliki admin authentication model;
checkpoint ini tidak menambah HTTP route.

## Istilah baru

- `read model` adalah bentuk data khusus pembacaan yang hanya memuat field yang aman
  dan diperlukan oleh satu output.
- `allowlist` adalah daftar eksplisit field yang boleh keluar, sehingga field baru
  tidak ikut terekspos secara otomatis.
- `tie-breaker` adalah key kedua yang membuat ordering tetap deterministik ketika dua
  record memiliki timestamp sama.
- `standard output` adalah stream untuk hasil command yang berhasil, sedangkan
  `standard error` adalah stream terpisah untuk diagnostic aman.

## Output contract

Success memakai satu JSON object pada standard output dengan exact top-level fields:

```json
{
  "sessionId": "<uuid>",
  "status": "expired",
  "events": [
    {
      "type": "expire",
      "occurredAt": "<RFC3339 timestamp>",
      "metadata": {}
    }
  ]
}
```

Events diurutkan ascending berdasarkan `occurred_at`, lalu event ID dipakai sebagai
tie-breaker internal tanpa ikut dicetak. Success exit code adalah `0`; invalid input,
not found, invalid stored bounded data, dan dependency failure memakai non-zero exit
serta safe diagnostic pada standard error.

## Candidate files

- `sql/queries/audit.sql`
- `internal/application/audit/service.go`
- `internal/adapter/postgres/audit.go`
- `cmd/audit/main.go`
- focused application, PostgreSQL, dan command tests

## Bagian user

Agent lebih dulu menyiapkan audit fixtures yang sengaja berisi token hash, Personal
Details, storage keys, provider payload, dan safe Session Events; user tidak menulis
redaction test matrix.

1. `[sql query]` Mulai dari session query yang menerima satu session ID. Pilih hanya
   `id` dan `status` dengan nama kolom eksplisit; jangan memakai `SELECT *` dan jangan
   membaca token hash, Personal Details, deadlines, rejection reason, atau field lain
   yang tidak ada dalam output contract.
2. `[sql query]` Buat query terpisah untuk Session Event history milik session yang
   sama. Pilih hanya event ID untuk tie-breaker internal, event type, safe metadata,
   dan `occurred_at`, lalu urutkan `occurred_at ASC, id ASC`; jangan join atau membaca
   raw Webhook Event payload.
3. `[verification]` Jalankan `make sqlc-generate` setelah kedua query selesai.
   Periksa generated result structs dan pastikan tidak ada sensitive column yang ikut
   terbawa; bila ada, kecilkan handwritten select list lalu generate ulang.
4. `[postgres adapter]` Implementasikan reader yang menjalankan session query lebih
   dulu dan mengubah no-row menjadi application-owned not-found outcome. Setelah itu
   baca ordered events, parse bounded state/event/metadata, dan hentikan operation
   bila database berisi nilai yang tidak dikenal.
5. `[application service]` Bentuk audit read model hanya dari safe session dan event
   records yang diberikan reader. Jangan meminta repository Personal Details, Upload
   Intent, Verification Artifact, atau Webhook Event karena data tersebut tidak
   dibutuhkan oleh output contract.
6. `[application service]` Susun output events sesuai urutan yang sudah diberikan
   adapter. Tampilkan hanya event type, `occurredAt` dalam UTC RFC3339, dan bounded
   metadata; simpan event ID sebagai detail internal untuk ordering dan jangan
   serialisasikan ke output.
7. `[runtime wiring]` Buat `cmd/audit` yang mewajibkan satu `--session-id`. Parse UUID
   sebelum membuka database, muat typed database config, buat reader/service, lalu
   jalankan operation dengan context timeout dan safe logger.
8. `[runtime wiring]` Atur output process secara eksplisit: JSON success hanya ke
   standard output, sedangkan invalid flag, not found, atau dependency failure hanya
   menjadi diagnostic aman di standard error. Tetapkan exit code konsisten untuk
   success, invalid input, not found, dan internal failure.
9. `[runtime wiring]` Tambahkan `make audit SESSION_ID=<uuid>` sebagai cara singkat
   menjalankan command. Target harus menunjukkan underlying `go run ./cmd/audit`
   invocation, meneruskan session ID sebagai flag, dan tetap mengambil database
   credential dari environment.
10. `[verification]` Jalankan focused application test lebih dulu, lalu PostgreSQL
    reader dan process-level command tracers. Inspeksi stdout, stderr, exit code,
    event ordering, serta fixture sensitif secara langsung untuk memastikan tidak ada
    field yang bocor.
11. `[review]` Berhenti setelah output success/error dan redaction evidence GREEN.
    Serahkan handwritten queries, generated diff, adapter, read model, CLI
    composition, exact JSON sample, stderr sample, dan command output kepada agent.

Kesalahan query allowlist, deterministic ordering, stdout/stderr separation, atau
sensitive-field exposure adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- tidak ada `SELECT *` atau query ke Personal Details/Webhook Event payload;
- output hanya memiliki exact documented fields;
- event metadata melewati bounded domain parser sebelum dicetak;
- same-timestamp events selalu memiliki ordering deterministik;
- unknown state/event tersimpan tidak dicetak sebagai arbitrary string;
- UUID, database, dan serialization errors tetap aman;
- command tidak memperkenalkan admin HTTP surface.

## Bagian agent

Setelah gate `[review]` lulus, agent menambahkan:

1. application and PostgreSQL redaction matrix dengan seluruh sensitive fixtures;
2. same-timestamp ordering, empty history, all bounded event types, dan metadata
   siblings;
3. invalid UUID, missing flag, unknown session, database unavailable, malformed
   stored data, cancellation, serta JSON write failure cases;
4. process-level stdout/stderr dan exact exit-code tests;
5. log assertions bahwa query errors, DSN, PII, token hash, storage key, URLs, dan
   raw payload tidak keluar;
6. focused race detector, vet, SQLC diff, dan `git diff --check`.

## Stop point

Checkpoint selesai ketika audit CLI menghasilkan exact safe JSON dan redaction matrix
GREEN. Health/readiness dan correlation logging tetap milik Checkpoint 6.
