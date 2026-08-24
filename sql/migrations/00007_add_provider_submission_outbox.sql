-- +goose Up

-- ISSUE 009 CHECKPOINT 1 — USER-OWNED MIGRATION SCAFFOLD
--
-- Add verification_deadline_at without changing applicant expires_at, then define
-- the bounded durable outbox described by the checkpoint. Keep this file
-- forward-only. The agent intentionally leaves the schema implementation empty.
alter table verification_sessions
add column verification_deadline_at timestamptz;

create table outbox (
  id uuid primary key,
  verification_session_id uuid not null,
  task_type text not null,
  payload jsonb NOT NULL,
  created_at timestamptz not null default now(),
  published_at timestamptz,
  claim_token uuid,
  claimed_until timestamptz,
  attempt_count int not null default 0,
  last_error_code text,
  constraint outbox_verification_session_fk
    foreign key (verification_session_id)
    references verification_sessions(id),
  constraint outbox_task_type_not_empty_check check (
    task_type = 'provider:submit'
  ),
  constraint outbox_payload_check check (
    payload = jsonb_build_object(
      'sessionId',
      verification_session_id::uuid
    )
  ),
  constraint outbox_claim_fields_coherent_check check (
    (
      claim_token is null
      and claimed_until is null
    )
    or
    (
      claim_token is not null
      and claimed_until is not null
      and published_at is null
    )
  ),
  constraint outbox_attempt_count check (
    attempt_count >= 0
  ),
  constraint outbox_last_error_code check (
    last_error_code in ('queue_publish_failed')
  )
);