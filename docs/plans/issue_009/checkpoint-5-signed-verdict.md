# Checkpoint 5 — Signed Verdict PostgreSQL Outcome

Status: aktif sejak 2026-08-27; review gates Checkpoint 1-2 selesai dan keputusan
unknown-session callback sudah dibekukan.

## Tujuan

Menerima exact Webhook Event shape yang sudah disetujui, melakukan strict decode
setelah HMAC valid, lalu atomically menyimpan first callback dan menerapkan verified
atau rejected Verification Verdict. Duplicate dan late application behavior sengaja
diselesaikan user di Checkpoint 8.

(`applied` dan `ignored` adalah processing status Webhook Event, bukan Verification
Session state.) Event `applied` menghasilkan satu state transition dan Session Event;
event `ignored` disimpan sebagai callback valid yang tidak boleh mengubah session.

## Exact Webhook Event contract

Verified:

```json
{"eventId":"<uuid>","sessionId":"<uuid>","verdict":"verified"}
```

Rejected:

```json
{
  "eventId":"<uuid>",
  "sessionId":"<uuid>",
  "verdict":"rejected",
  "reason":"document_invalid"
}
```

Rejected reasons tepat:

```text
document_invalid
biometric_mismatch
identity_not_verified
suspected_fraud
```

Verified event tidak boleh membawa `reason`; rejected event wajib membawa satu
bounded reason. Duplicate callback memakai exact `eventId` yang sama. Callback baru
dengan `eventId` berbeda setelah session tidak lagi pending adalah late/out-of-order,
bukan duplicate.

## Keputusan unknown-session — dibekukan 2026-08-27

Signature-valid dan structurally-valid callback dengan `sessionId` yang tidak dikenal
disimpan tepat sekali sebagai Webhook Event dengan processing status `ignored` dan
bounded reason `unknown_session`. Ia mengembalikan exact
`200 {"status":"ok"}`, tidak mengubah Verification Session, dan tidak membuat Session
Event. Ignored outcome bersifat final dan tidak otomatis diterapkan bila session
dengan UUID tersebut muncul kemudian.

Provider-reported UUID disimpan sebagai `reported_session_id UUID NOT NULL` tanpa
foreign key ke `verification_sessions`. Kolom ini merekam identifier yang dinyatakan
provider, bukan menjamin aggregate ada. Valid raw payload tetap disimpan exact dan
provider `eventId` tetap menjadi primary deduplication key. Replay event ID yang sama
kelak mengembalikan `200` tanpa mutation tambahan; minimal duplicate behavior tetap
diimplementasikan bersama duplicate/late matrix Checkpoint 8.

(`structurally valid` berarti JSON mempunyai exact fields dan bounded values yang
benar, meskipun referenced session mungkin tidak ada.)

## Candidate files

- `sql/migrations/00008_create_webhook_events.sql`
- `sql/queries/webhook_events.sql`
- `sql/queries/provider_verdicts.sql`
- `sql/proofs/009_signed_provider_verdicts.sql`
- `internal/domain/providerverdict/`
- `internal/application/providerverdict/service.go`
- `internal/adapter/postgres/provider_verdict_transactions.go`
- `internal/adapter/httpapi/webhook.go`
- focused PostgreSQL/HTTP integration tests

## Bagian user

Setelah decision gate ditutup, agent menyiapkan RED verified tracer, disposable
PostgreSQL runner, raw-body HMAC helper, dan repetitive fixture data. User menulis
schema/query/domain/application transaction untuk verified dan, sesuai approval,
rejected path beserta empat reasons.

1. `[domain]` User mendefinisikan bounded verdict `verified|rejected` dan exact four
   rejection reasons tanpa free-form provider result.
2. `[domain]` User menulis parser/constructor rule bahwa verified tidak memiliki
   reason dan rejected wajib memiliki satu bounded reason.
3. `[migration]` User membuat `webhook_events` dengan provider event ID
   deduplication, `reported_session_id UUID NOT NULL` tanpa foreign key, exact valid
   raw payload, processing status `applied|ignored`, bounded ignore reason termasuk
   `unknown_session`, serta received/processed timestamps.
4. `[migration]` User menambahkan `verified_at`, `rejected_at`, dan bounded
   `rejection_reason` ke `verification_sessions` dengan coherent terminal-field
   constraints.
5. `[sql query][postgres adapter]` User menulis insert-or-detect-duplicate Webhook
   Event query untuk exact provider `eventId`.
6. `[sql query][postgres adapter]` User menulis guarded verified update dari
   `verification_pending` ke `verified` beserta `verified_at`.
7. `[sql query][postgres adapter]` User menulis guarded rejected update dari
   `verification_pending` ke `rejected` beserta `rejected_at` dan exact reason.
8. `[sql query][postgres adapter]` User menulis query untuk menandai event `applied`
   atau bounded `ignored` di transaction yang sama.
9. `[verification]` User menjalankan `sqlc generate` dan memeriksa generated types
   tanpa manual edit.
10. `[application service]` User menulis strict event decode/validation setelah
    Checkpoint 2 HMAC gate dan menghasilkan application-owned verdict input.
11. `[application service]` User menulis `VerdictService.Apply` verified transaction:
    insert first event, lock/re-read session, guard pending, update terminal fields,
    append `verification_passed`, dan mark event applied; duplicate marker belum
    ditangani menjadi replay sampai Checkpoint 8.
12. `[verification]` User menjalankan agent-provided verified tracer sampai GREEN.
13. `[application service]` User menambahkan rejected transaction memakai shared
    orchestration tetapi exact `verification_failed` action dan bounded reason.
14. `[integration test][application service]` User membuktikan keempat rejected
    reasons melalui public signed webhook path dan durable PostgreSQL outcome.
15. `[verification]` User menjalankan rejected suite sampai GREEN dan memeriksa tidak
    ada free-form reason yang tersimpan.
16. `[sql proof][sql query]` User menulis success proof minimum untuk one verified dan
    one rejected atomic outcome.
17. `[review]` User berhenti sebelum malformed/rollback sibling matrix; duplicate dan
    late callback tetap user-owned Checkpoint 8.

Kesalahan event type, terminal timestamps, rejection reason bounds, atau partial
state/event/webhook writes adalah concept-bearing dan dikembalikan kepada user.

## Review agent

Agent memeriksa:

- HMAC tetap diverifikasi sebelum strict JSON decode dan persistence;
- raw valid payload memiliki hard size bound dan tidak masuk log;
- `eventId` adalah deduplication key, sedangkan `sessionId` memilih aggregate;
- `reported_session_id` tetap non-null tetapi sengaja bukan foreign key karena valid
  unknown-session event adalah durable integration outcome;
- schema/query surface dapat mendukung duplicate/ignored continuation tanpa
  mengimplementasikan behavior itu lebih awal;
- verified/rejected action memakai existing domain transition map;
- all verdict/webhook/session writes memakai satu transaction-bound query set;
- `ignored` tidak menjadi public state atau Session Event type;
- session terminal tidak dapat berubah oleh callback berikutnya.

## Bagian agent

Setelah verified/rejected user paths direview, agent menutup:

1. malformed signed JSON, unknown fields, multiple values, invalid UUID/verdict/
   reason combinations tidak tersimpan;
2. forced Session Event atau Webhook Event status failure me-roll back seluruh verdict;
3. invalid-signature no-row proof dari Checkpoint 2 tetap GREEN;
4. focused PostgreSQL/HTTP rollback suite dan existing terminal-state regressions;
5. first valid unknown-session callback disimpan `ignored:unknown_session`, mendapat
   exact `200 {"status":"ok"}`, dan tidak membuat session mutation atau Session Event.

## Definition of done

- unknown-session contract di atas dibuktikan melalui signed public path;
- user-authored domain/schema/query/verified/rejected code direview;
- keempat rejected reasons terbukti melalui signed public path;
- applied/ignored tetap Webhook Event processing status;
- first verified/rejected callback commit/rollback terbukti atomik;
- duplicate/late behavior tetap terbuka dan eksplisit dimiliki user di Checkpoint 8;
- PostgreSQL proof, race detector, `sqlc-diff`, dan migration validation GREEN.
