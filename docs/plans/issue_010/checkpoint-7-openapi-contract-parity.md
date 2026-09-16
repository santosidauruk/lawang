# Checkpoint 7 — OpenAPI Contract Parity

Status: menunggu public contracts Checkpoint 1-6 dibekukan dan seluruh gate
`[review]` lulus.

## Tujuan

Menerbitkan `docs/openapi.yaml` untuk seluruh public HTTP routes Issue 001-010 dan
membuktikan method, authentication, schemas, statuses, replay/conflict behavior, serta
error examples cocok dengan handler dan integration evidence aktual.

User mereview public contract; agent menulis repetitive OpenAPI schemas, examples,
lint checks, dan parity regressions setelah review lulus.

## Istilah baru

- `OpenAPI` adalah format machine-readable untuk mendeskripsikan public HTTP
  operations, request/response schemas, authentication, dan errors.
- `schema` adalah aturan bentuk serta tipe field yang valid untuk satu request atau
  response.
- `lint` adalah pemeriksaan otomatis terhadap struktur dan konsistensi dokumen tanpa
  menjalankan business flow.
- `contract drift` adalah perbedaan antara dokumentasi publik dan perilaku runtime
  yang benar-benar diamati.
- `source of truth matrix` adalah tabel yang menunjuk test/evidence pemilik setiap
  method, status, field, replay, dan error contract.

## Surface yang harus tercakup

- `GET /health/live` dan `GET /health/ready`;
- create dan resume Verification Session;
- submit immutable Personal Details;
- create Upload Intent dan confirm Identity Document/Biometric Capture;
- submit Verification Session;
- signed provider webhook;
- seluruh public states, bounded rejection reasons, auth schemes, replay/conflict
  rules, request limits, success responses, dan safe error envelope;
- audit dan cleanup dinyatakan sebagai CLI commands, bukan HTTP paths.

## Candidate files

- `docs/openapi.yaml`
- `docs/openapi-source-matrix.md` bila matrix tidak muat secara jelas di checkpoint
- OpenAPI lint/parity tests under existing HTTP test packages or `tests/contract/`
- `Makefile` target yang menampilkan underlying lint/parity command

## Bagian user

Agent lebih dulu membuat route inventory dan source-of-truth matrix dari handler,
integration tests, Issue documents, dan completion evidence; agent belum menutup
repetitive schemas sebelum review user.

1. `[api contract]` Mulai dari route inventory yang disiapkan agent dan cocokkan satu
   per satu dengan route registration aktual. Tandai route publik yang belum masuk,
   method yang salah, atau path yang tidak ada; pastikan `audit` dan
   `cleanup-orphans` tetap tercatat sebagai CLI, bukan diterbitkan sebagai HTTP path.
2. `[api contract]` Untuk setiap route, baca authentication requirement dari handler
   dan integration evidence. Catat applicant routes yang memakai Bearer resume token,
   webhook yang memakai HMAC signature, serta health routes tanpa authentication;
   pastikan scheme tersebut ditulis pada operation yang tepat.
3. `[api contract]` Review success contract route demi route, mulai dari create dan
   resume session lalu lanjut ke Personal Details, upload URL, confirm, submit,
   webhook, serta health. Cocokkan exact HTTP status, content type, required fields,
   conditional fields, dan public field names dengan response yang benar-benar
   direkam test.
4. `[api contract]` Review error contract setelah success paths selesai. Untuk setiap
   route, cocokkan status, machine-readable code, message, details, body limit,
   malformed JSON, replay, conflict, expiry, dan terminal behavior; jangan
   menyimpulkan error hanya dari nama Go type.
5. `[api contract]` Kumpulkan seluruh bounded values yang muncul di public contract:
   session states, artifact kinds, provider verdicts, rejection reasons, dan error
   codes. Cocokkan dengan domain constants serta tests, lalu hapus value yang tidak
   memiliki evidence agar OpenAPI tidak menciptakan enum baru.
6. `[api contract]` Bandingkan setiap request/response example dengan recorded
   integration output yang aman. Bila ada mismatch, catat route, field/status yang
   berbeda, source of truth pemiliknya, dan keputusan yang dibutuhkan; jangan
   memperbaiki runtime atau dokumentasi secara diam-diam selama review.
7. `[review]` Berhenti setelah route, auth, success, error, enum, dan examples selesai
   direview serta seluruh mismatch tercatat. Serahkan annotated inventory dan
   keputusan contract kepada agent sebelum agent menutup repetitive OpenAPI schemas,
   lint, dan parity checks.

Kesalahan method/status/auth/state yang ditemukan tidak boleh diselesaikan dengan
diam-diam mengubah application behavior. Konflik sumber harus ditunjukkan dan
diputuskan pada source of truth yang benar.

## Review agent

Agent memeriksa hasil user terhadap:

- `internal/adapter/httpapi` route registration dan strict decoders;
- integration evidence Issue 001-009 dan health contract Checkpoint 6;
- public error envelope serta exact conditional fields;
- replay/conflict/terminal behavior;
- scope boundary bahwa audit/cleanup bukan HTTP routes;
- no sensitive examples atau real credentials/PII.

## Bagian agent

Setelah gate `[review]` lulus, agent:

1. menulis reusable schemas dan route operations di `docs/openapi.yaml`;
2. menambahkan exact success/error examples dari safe test fixtures;
3. menambahkan lint target dengan tool/version yang dipin dan underlying command yang
   terlihat;
4. menambahkan parity checks untuk route/method/status/content type dan selected
   request/response schemas;
5. memperbaiki repetitive schema/example drift tanpa mengambil keputusan domain baru;
6. menjalankan OpenAPI lint, HTTP contract suites, race detector yang relevan, dan
   `git diff --check`.

## Stop point

Checkpoint selesai ketika OpenAPI lint/parity GREEN, semua review findings ditutup,
dan tidak ada undocumented public route. Packaging, CI, serta fresh-clone proof tetap
milik Checkpoint 8.
