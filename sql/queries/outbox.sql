-- ISSUE 009 CHECKPOINT 1 — USER-OWNED QUERY SCAFFOLD
--
-- Add the first unpublished provider:submit outbox insert named query here. Keep its
-- JSON payload identifier-only and supply its UUID from application code.
-- name: InsertUnpublishedOutbox :exec
insert into outbox (
  id,
  verification_session_id,
  task_type,
  payload
) values (
  sqlc.arg(id),
  sqlc.arg(verification_session_id),
  'provider:submit',
  jsonb_build_object('sessionId', sqlc.arg(verification_session_id)::uuid)
);

-- name: ClaimNextOutbox :one
select id, verification_session_id, task_type, payload
from outbox
where task_type = 'provider:submit' and published_at is null and (claimed_until is null or claimed_until <= sqlc.arg(claimed_at)::timestamptz)
order by created_at, id
limit 1
for update skip locked;

-- name: FillClaimTokenOutbox :execrows
update outbox
set
  claim_token = sqlc.arg(claim_token),
  claimed_until = sqlc.arg(claimed_until)::timestamptz,
  attempt_count = attempt_count + 1
where published_at is null and id = sqlc.arg(outbox_id);

-- name: MarkPublishedOutbox :execrows
update outbox
set
  published_at = now(),
  claim_token = null,
  claimed_until = null,
  last_error_code = null
where id = sqlc.arg(outbox_id) and published_at is null and claim_token = sqlc.arg(claim_token);

-- name: RecordOutboxPublishFailure :execrows
update outbox
set last_error_code = 'queue_publish_failed'
where id = sqlc.arg(outbox_id)
  and published_at is null
  and claim_token = sqlc.arg(claim_token);
