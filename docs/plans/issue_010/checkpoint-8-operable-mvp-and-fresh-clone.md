# Checkpoint 8 — Operable MVP dan Fresh-Clone Proof

Status: menunggu Checkpoint 1-7 selesai, seluruh gate `[review]` lulus, dan perubahan
sebelumnya tersedia dalam commit yang dapat di-checkout.

## Tujuan

Menutup Issue 010 dengan transparent command surface, complete container topology,
CI quality gates, full success/rejection E2E, dan fresh-clone bootstrap/demo evidence.

Checkpoint ini tidak menambah domain abstraction baru hanya agar packaging atau demo
lulus. Production deployment dan production-readiness claim tetap di luar scope.

## Istilah baru

- `bootstrap` adalah langkah eksplisit untuk menyiapkan dependency, bucket, dan
  database schema dari checkout baru.
- `one-shot command` adalah process yang menjalankan satu operasi lalu keluar dengan
  status berhasil atau gagal.
- `silent skip` adalah test yang dilaporkan lulus walaupun dependency-backed scenario
  sebenarnya tidak dijalankan.
- `clean environment` adalah lingkungan tanpa volume, cache, binary, atau generated
  configuration dari run sebelumnya.
- `smoke demonstration` adalah perjalanan singkat melalui public path utama untuk
  membuktikan seluruh process dapat bekerja bersama.

## Final observable flow

Dokumentasi harus membawa contributor baru melalui:

1. install pinned tools;
2. start PostgreSQL, MinIO, dan Redis;
3. bootstrap bucket dan migrate database dari zero;
4. start API, worker, dan fake provider;
5. create session dan submit Personal Details;
6. upload/confirm Identity Document dan Biometric Capture;
7. submit lalu mencapai verified serta setiap bounded rejected outcome;
8. menjalankan audit CLI;
9. menjalankan manual cleanup pada safe orphan fixture;
10. membuktikan health, structured logs, scheduled expiry/cleanup, dan graceful stop.

## Candidate files

- `Dockerfile`
- `compose.yaml`
- `.env.example`
- `Makefile`
- `.github/workflows/quality.yml`
- `README.md`
- `docs/learning/010-operable-mvp.md`
- final integration/E2E tests dan fresh-clone transcript

## Bagian user

Agent lebih dulu membuat command/topology inventory dan draft runbook berdasarkan
production binaries serta focused evidence Checkpoint 1-7; user tidak menulis E2E
test atau CI matrix.

1. `[runtime wiring]` Mulai dari setiap binary secara terpisah: API, worker,
   fake-provider, audit, dan cleanup. Ikuti constructor chain dari config sampai
   process start, lalu catat dependency yang benar-benar dipakai; tandai binary yang
   menerima config/client berlebih atau membuat application service berbeda dari
   service yang sudah direview.
2. `[runtime wiring]` Review Dockerfile targets terlebih dahulu, kemudian cocokkan
   hasil binary-nya dengan Compose services dan profiles. Pastikan API, worker, dan
   fake-provider menjadi long-running services, sedangkan audit serta cleanup hanya
   berjalan sebagai one-shot commands dan keluar dengan status yang dapat diperiksa.
3. `[runtime wiring]` Buat daftar seluruh environment variables per process. Untuk
   setiap value, tandai apakah required, secret, local default, duration, address,
   concurrency, interval, atau batch size; cari nama/default yang tidak konsisten dan
   pastikan process tidak menerima secret yang tidak digunakannya.
4. `[runtime wiring]` Jalankan inventory Make targets dari setup hingga quality:
   bootstrap, migrate, API, worker, provider, audit, cleanup, integration, E2E,
   Docker build, dan quality. Untuk setiap target, pastikan underlying standard
   command terlihat, namanya tidak ambigu, dan tidak ada destructive reset tersembunyi.
5. `[documentation]` Ikuti draft fresh-clone runbook seolah-olah workspace lama tidak
   tersedia. Periksa urutan install tools, copy environment example, start
   dependencies, bootstrap bucket, migrate database, start processes, run demo,
   audit, cleanup, dan shutdown; tandai setiap langkah yang masih membutuhkan
   knowledge, file, volume, atau command yang tidak tertulis.
6. `[documentation]` Baca README dan deployment/security notes sebagai satu rangkaian.
   Pastikan dokumen menyebut plaintext local PII, local-development secrets, belum
   adanya KMS, production IAM, malware scan, retention workflow, metrics,
   OpenTelemetry, dan threat model, serta kebutuhan reverse-proxy HTTPS untuk
   lingkungan non-lokal; hapus klaim production-ready atau compliant.
7. `[review]` Berhenti setelah binary composition, container topology, environment
   matrix, Make targets, runbook, dan limitation review selesai. Serahkan daftar
   findings beserta file/section pemiliknya kepada agent; agent yang mengerjakan CI,
   repetitive E2E matrix, final quality run, dan fresh-clone evidence.

Review user tidak dianggap sebagai fresh-clone proof; agent tetap harus menjalankan
flow pada clean environment dan merekam command/output aktual.

## Review agent

Agent memeriksa:

- no hidden destructive reset command;
- no credentials atau real PII committed ke config/examples;
- Compose dependency conditions hanya menjelaskan startup readiness, bukan runtime
  recovery guarantee;
- one-shot commands memakai application use cases yang sudah direview;
- CI menjalankan dependency-backed tests tanpa `t.Skip`;
- fresh-clone evidence berasal dari commit yang dapat di-checkout;
- README tidak menyebut MVP production-ready atau compliant.

## Bagian agent

Setelah gate `[review]` lulus, agent:

1. melengkapi Docker build targets dan Compose one-shot operational commands;
2. melengkapi `.env.example`, Makefile, README, bootstrap, dan migration workflow;
3. menambahkan pinned CI workflow untuk formatting, vet, staticcheck, race tests,
   SQLC diff, Goose validation, OpenAPI lint/parity, Compose validation, Docker build,
   integration, dan E2E;
4. menambahkan final success plus four rejected E2E scenarios serta expiry, cleanup,
   audit, health, and safe-log assertions;
5. membuktikan dependency-backed suites fail clearly ketika dependency unavailable,
   bukan silent skip;
6. menjalankan Docker build dan complete quality gate dengan timeout yang cukup;
7. membuat temporary fresh clone dari commit yang disetujui, menjalankan documented
   bootstrap/demo tanpa existing volumes, lalu merekam transcript;
8. menulis `docs/learning/010-operable-mvp.md` dengan keputusan, actual commands,
   versions, outputs, recovery behavior, dan deferred limitations;
9. memperbarui parent Issue 010 menjadi `done` hanya setelah seluruh evidence GREEN.

## Final gate

Issue 010 selesai hanya bila:

- user-owned cleanup SQL proof dan OpenAPI review gates lulus;
- all focused checkpoint suites dan full `make quality` GREEN;
- generated SQLC diff, migration validation, OpenAPI parity, Compose validation, dan
  seluruh Docker builds GREEN;
- real PostgreSQL, MinIO, Redis, API, worker, serta fake-provider E2E tidak skip;
- fresh clone dapat menjalankan documented verified/rejected demo, audit, dan cleanup;
- learning note serta README menyatakan production limitations secara eksplisit.
