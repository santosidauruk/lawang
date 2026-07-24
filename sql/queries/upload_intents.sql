-- CHECKPOINT 3 STEP 3 — USER-AUTHORED FIRST QUERY (COMPLETE)
--
-- name: LockUploadIntent :one
select id, verification_session_id, kind, storage_key, status, created_at, latest_status_change_at, expires_at, confirmed_at, object_deleted_at, failure_code
from upload_intents
where verification_session_id = sqlc.arg(verification_session_id) and id = sqlc.arg(upload_intent_id)
for update;

-- CHECKPOINT 3 STEP 4 — USER-AUTHORED SUCCESS-PATH QUERIES
--
-- Add these queries one at a time and run `make sqlc-generate` after each:
--
-- 1. LoadUploadIntent :one
--    - non-locking read used before external object storage/extractor calls;
--    - receives Verification Session ID and Upload Intent ID;
--    - enforces ownership with both identifiers;
--    - returns the same columns as LockUploadIntent.
--
-- 2. ConfirmUploadIntent :execrows
--    - sets status to confirmed;
--    - sets confirmed_at and latest_status_change_at from the application time;
--    - updates only the requested ID in expected pending state;
--    - the adapter must reject any affected-row count other than one as stale.
--
-- 3. MarkUploadIntentValidationFailed :execrows
--    - required by artifact.Transaction even though the current tracer follows the
--      accepted branch;
--    - sets status, failure_code, and latest_status_change_at;
--    - updates only the requested ID in expected pending state;
--    - the adapter must reject any affected-row count other than one as stale.
--
-- Do not add create/supersede Upload Intent queries in this tracer. Those belong to
-- the later create-upload behavior listed in the checkpoint plan.

-- name: LoadUploadIntent :one
select id, verification_session_id, kind, storage_key, status, created_at, latest_status_change_at, expires_at, confirmed_at, object_deleted_at, failure_code
from upload_intents
where verification_session_id = sqlc.arg(verification_session_id) and id = sqlc.arg(upload_intent_id);

-- name: ConfirmUploadIntent :execrows
update upload_intents
set status = 'confirmed', confirmed_at = sqlc.arg(upload_intent_confirmed_at), latest_status_change_at = sqlc.arg(upload_intent_latest_status_change_at)
where status = 'pending' and id = sqlc.arg(upload_intent_id);

-- name: MarkUploadIntentValidationFailed :execrows
update upload_intents 
set status = 'validation_failed', failure_code = sqlc.arg(upload_intent_failure_code), latest_status_change_at = sqlc.arg(upload_intent_latest_status_change_at)
where status = 'pending' and id = sqlc.arg(upload_intent_id);

-- name: SupersedePendingUploadIntent :execrows
UPDATE upload_intents
SET
    status = 'superseded',
    latest_status_change_at = sqlc.arg(superseded_at)
WHERE verification_session_id = sqlc.arg(verification_session_id)
  AND kind = sqlc.arg(kind)
  AND status = 'pending';

-- name: InsertUploadIntent :exec
INSERT INTO upload_intents (
    id,
    verification_session_id,
    kind,
    storage_key,
    status,
    created_at,
    latest_status_change_at,
    expires_at
) VALUES (
    sqlc.arg(upload_intent_id),
    sqlc.arg(verification_session_id),
    sqlc.arg(kind),
    sqlc.arg(storage_key),
    'pending',
    sqlc.arg(created_at),
    sqlc.arg(latest_status_change_at),
    sqlc.arg(expires_at)
);
