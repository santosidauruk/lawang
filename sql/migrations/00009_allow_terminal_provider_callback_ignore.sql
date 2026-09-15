-- +goose Up

alter table webhook_events
drop constraint webhook_events_ignore_reason_check,
drop constraint webhook_events_status_ignore_reason_processed_at;

alter table webhook_events
add constraint webhook_events_ignore_reason_check check (
  ignore_reason in ('unknown_session', 'terminal_session')
),
add constraint webhook_events_status_ignore_reason_processed_at check (
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
    and ignore_reason in ('unknown_session', 'terminal_session')
  )
);
