-- +goose Up

-- ISSUE 009 CHECKPOINT 5 — USER-OWNED MIGRATION SCAFFOLD
--
-- Define Webhook Event persistence and the Verification Session terminal verdict
-- fields required by docs/plans/issue_009/checkpoint-5-signed-verdict.md.
-- Keep this migration forward-only. The agent intentionally leaves the schema
-- implementation empty so the first signed-verdict migration remains user-owned.

-- User membuat `webhook_events` dengan provider event ID
-- deduplication, `reported_session_id UUID NOT NULL` tanpa foreign key, exact valid
-- raw payload, processing status `applied|ignored`, bounded ignore reason termasuk
-- `unknown_session`, serta received/processed timestamps.
create table webhook_events (
  id uuid primary key,
  reported_session_id uuid not null,
  payload bytea not null,
  created_at timestamptz not null default now(),
  processing_status text,
  ignore_reason text,
  received_at timestamptz not null,
  processed_at timestamptz,
  constraint webhook_events_processing_status_check check (
    processing_status in ('applied', 'ignored')
  ),
  constraint webhook_events_ignore_reason_check check (
    ignore_reason in ('unknown_session')
  ),
  constraint webhook_events_status_ignore_reason_processed_at check (
    (
      processing_status is null
      and processed_at is null
      and ignore_reason is null
    )
    or
    (
      processing_status = 'applied'
      and processed_at is not null
      and ignore_reason is null
    )
    or
    (
      processing_status = 'ignored'
      and processed_at is not null
      and ignore_reason is not null
      and ignore_reason in ('unknown_session')
    )
  )
);

-- `[migration]` User menambahkan `verified_at`, `rejected_at`, dan bounded
--    `rejection_reason` ke `verification_sessions` dengan coherent terminal-field
--    constraints.
alter table verification_sessions
add column verified_at timestamptz,
add column rejected_at timestamptz,
add column rejection_reason text,
add constraint verification_sessions_rejection_reason_check check (
  rejection_reason in ('document_invalid', 'biometric_mismatch', 'identity_not_verified', 'suspected_fraud')
),
add constraint verification_sessions_verdict_field_check check (
  (
    status = 'verified'
    and verified_at is not null
    and rejected_at is null
    and rejection_reason is null
  )
  OR
  (
    status = 'rejected'
    and verified_at is null
    and rejected_at is not null
    and rejection_reason is not null
    and rejection_reason in ('document_invalid', 'biometric_mismatch', 'identity_not_verified', 'suspected_fraud')
  )
  OR
  (
    status not in ('verified', 'rejected')
    and verified_at is null
    and rejected_at is null
    and rejection_reason is null
  )
);
