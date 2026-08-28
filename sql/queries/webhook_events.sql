-- ISSUE 009 CHECKPOINT 5 — USER-OWNED QUERY SCAFFOLD
--
-- Add the handwritten sqlc queries for inserting/detecting provider event IDs and
-- marking an event applied or bounded ignored. Do not edit generated sqlc files.


-- 5. `[sql query][postgres adapter]` User menulis insert-or-detect-duplicate Webhook
-- Event query untuk exact provider `eventId`.
-- name: InsertWebhookEvent :one
insert into webhook_events (
  id,
  reported_session_id,
  payload,
  received_at
)
values (
  sqlc.arg(webhook_event_id),
  sqlc.arg(session_id),
  sqlc.arg(payload),
  sqlc.arg(received_at)
)
on conflict (id) do nothing
returning id;


-- 8. `[sql query][postgres adapter]` User menulis query untuk menandai event `applied`
--    atau bounded `ignored` di transaction yang sama.
-- name: MarkWebhookEvents :execrows
update webhook_events
set
  processing_status = sqlc.arg(processing_status),
  processed_at = sqlc.arg(processed_at),
  ignore_reason = case
    when sqlc.arg(processing_status) = 'ignored' then sqlc.arg(ignore_reason)
    else null
  end
where id = sqlc.arg(webhook_event_id) and processing_status is null;
