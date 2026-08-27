# Checkpoint 4 — Concurrent Outbox Relay ke Redis

Status: selesai pada 2026-08-27; claim/relay/queue slice user direview dan seluruh
idle, outage, crash-window, reclaim, duplicate, serta safety matrix agent GREEN.

## Tujuan

Memindahkan unpublished `provider:submit` outbox rows dari PostgreSQL ke Redis/Asynq
dengan stable TaskID dan short claim lease yang aman untuk beberapa relay concurrent.

(`claim lease` adalah hak sementara atas satu outbox row yang dapat diambil relay
lain setelah kedaluwarsa.) (`crash window` adalah jeda antara enqueue sukses dan
pencatatan `published_at`, ketika satu sistem sudah berubah tetapi sistem lain belum.)

Checkpoint ini berhenti setelah task ada di Redis. Ia belum menjalankan provider task.

## Perilaku tracer

- relay claim memakai short PostgreSQL transaction;
- transaction ditutup sebelum enqueue ke Redis;
- outbox UUID menjadi exact Asynq TaskID;
- enqueue success atau duplicate TaskID menandai row published;
- dua relay concurrent tidak memiliki claim aktif yang sah atas row sama;
- stale relay tidak dapat mark-published memakai claim token lama;
- crash/failure meninggalkan row yang dapat direclaim setelah lease expiry.

## Candidate files

- `sql/queries/outbox.sql`
- `internal/application/outbox/relay.go`
- `internal/adapter/postgres/outbox.go`
- `internal/adapter/queue/asynq.go`
- `cmd/worker/main.go` hanya bila minimum process shell dibutuhkan; full worker wiring
  milik Checkpoint 7
- PostgreSQL + Redis integration tests

## Bagian user

Agent menyiapkan disposable PostgreSQL/Redis fixture, single-relay RED tracer, dan
inspection helpers. User menulis claim/relay/queue production code serta concurrent
relay proof yang secara eksplisit dipilih sebagai learning slice.

1. `[sql query][postgres adapter]` User menulis query untuk memilih satu eligible
   unpublished row dengan `FOR UPDATE SKIP LOCKED`.
2. `[sql query][postgres adapter]` User menulis query yang menetapkan fresh
   `claim_token`, `claimed_until`, dan attempt metadata dalam short transaction.
3. `[sql query][postgres adapter]` User menulis mark-published query yang hanya sukses
   bila row masih dimiliki exact `claim_token`.
4. `[postgres adapter]` User menulis `ClaimNext`, `MarkPublished`, dan safe failure
   recording operations tanpa Redis I/O di dalam transaction.
5. `[application service]` User mendefinisikan queue publisher port yang hanya
   menerima application-owned task ID, type, dan identifier-only payload.
6. `[application service]` User menulis minimal `Relay.RunOnce`: claim, enqueue, lalu
   mark published.
7. `[queue adapter]` User menulis Asynq adapter operation yang memakai outbox UUID
   sebagai `TaskID` dan memetakan duplicate TaskID menjadi successful publication.
8. `[verification]` User menjalankan single-relay real Redis tracer sampai GREEN.
9. `[integration test][queue adapter]` User menulis concurrent-relay tracer dengan dua
   relay instances, separate PostgreSQL connections, coordinated start, dan tanpa
   `time.Sleep` sebagai bukti winner.
10. `[integration test][postgres adapter]` User mengassert hanya satu valid claim
    owner pada satu waktu dan stale token tidak dapat mark published.
11. `[integration test][queue adapter]` User mengassert Redis menerima satu logical
    task dengan exact outbox TaskID dan outbox akhirnya published.
12. `[verification]` User menjalankan concurrent tracer dengan race detector sampai
    GREEN dan memastikan tidak ada connection/goroutine leak.
13. `[review]` User berhenti sebelum crash, lease-expiry, dan retry sibling matrix.

Kesalahan transaction yang tetap terbuka selama Redis I/O, claim tanpa expiry, atau
mark-published tanpa token ownership adalah concept-bearing dan dikembalikan kepada
user.

## Review agent

Agent memeriksa:

- `SKIP LOCKED` dipakai untuk throughput, bukan sebagai pengganti durable lease;
- claim token unik per attempt dan compared saat mark;
- database time atau injected trusted clock dipakai konsisten untuk eligibility;
- Redis/Asynq types berhenti di queue adapter;
- cancellation tidak meninggalkan transaction atau connection leak;
- duplicate TaskID bukan klaim exactly-once;
- concurrent test benar-benar mempertemukan dua relay, bukan dua goroutine yang
  berjalan serial secara kebetulan.

## Review gate evidence

Review Bagian user lulus pada 2026-08-27. Claim memakai transaction PostgreSQL
pendek dan commit sebelum publisher dipanggil; exact token memagari publication;
application port hanya membawa UUID, task type, dan identifier-only payload; serta
Asynq tetap berada di queue adapter. User-authored concurrent tracer menjalankan dua
relay dengan connection terpisah, start gate tanpa `time.Sleep`, active-lease
inspection dari connection lain, non-owner mark rejection, exact one-winner
publication, dan goroutine cleanup yang menunggu kedua actor sebelum resource
dilepas.

Focused user verification GREEN dengan race detector untuk single-relay dan
concurrent-relay tracer. Review juga mengoreksi no-row dari raw `pgx.ErrNoRows`
menjadi application-owned idle outcome pada continuation agent.

## Bagian agent — selesai

Setelah concurrent tracer user direview, agent menutup:

1. no-row idle behavior tanpa busy loop di application operation;
2. Redis unavailable meninggalkan unpublished row yang dapat dicoba kembali;
3. crash/failure sebelum enqueue dan setelah enqueue/sebelum mark-published;
4. expired lease dapat direclaim dan old claim token tidak dapat menang terlambat;
5. duplicate TaskID conflict menandai publication sukses;
6. malformed/unknown outbox task tidak menyebabkan unsafe payload/logging;
7. claim attempt/error metadata tetap bounded;
8. exact pinned Asynq dependency, PostgreSQL/Redis race suite, dan restart-safe
   integration verification.

## Definition of done

- user-authored claim queries, adapter, relay, queue operation, dan concurrent proof
  direview;
- no external I/O terjadi dalam PostgreSQL transaction;
- Redis outage/crash tidak menghilangkan unpublished work;
- outbox UUID menjadi exact TaskID;
- published hanya berarti handoff ke queue selesai;
- real PostgreSQL + Redis race suite GREEN tanpa silent skip.

## Completion evidence

Bagian agent menutup no-row idle tanpa loop internal; malformed/unknown task ditolak
sebelum Redis; Redis outage menyimpan hanya bounded `queue_publish_failed` dan
meninggalkan unpublished claim untuk retry setelah lease; successful retry
membersihkan failure metadata. Crash setelah Redis enqueue tetapi sebelum
`MarkPublished` direproduksi, lalu relay baru mereclaim sesudah expiry. Token lama
ditolak setelah token baru tersimpan, duplicate Asynq TaskID diperlakukan sebagai
successful handoff, row akhirnya published, dan Redis tetap memiliki tepat satu
logical task dengan outbox UUID sebagai TaskID.

Verification GREEN pada 2026-08-27:

- application/postgres/queue unit suite dengan race detector;
- enam focused PostgreSQL + Redis outbox relay tracers dengan race detector,
  termasuk user-owned single/concurrent proof dan agent idle/outage/crash/duplicate
  matrix;
- full `make quality`, termasuk `go test -race ./...`, integration package selesai
  dalam 556.641 detik dan schema package dalam 18.231 detik;
- `go vet`, Staticcheck, `make sqlc-diff`, `make migration-validate`,
  `make compose-validate`, dan `git diff --check`;
- PostgreSQL 18.4 dari `postgres:18.4-alpine3.23`, Redis 7.4.11 dari
  `redis:7.4-alpine`, dan exact direct Asynq dependency `v0.26.0`.

Checkpoint ini tetap berhenti pada PostgreSQL -> Redis handoff. Ia tidak menjalankan
provider task dan tidak menambahkan worker runtime wiring, yang tetap milik
Checkpoint 7.
