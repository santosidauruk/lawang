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
