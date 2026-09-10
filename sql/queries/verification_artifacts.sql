-- CHECKPOINT 3 STEP 4 — USER-AUTHORED SUCCESS-PATH QUERY
--
-- Write exactly one query for the accepted confirmation tracer:
--
-- Insert every field of artifact.VerificationArtifact explicitly:
--   id, upload_intent_id, verification_session_id, kind, storage_key,
--   content_type, size_bytes, etag, created_at.
--
-- Do not use database-generated ID/time values. The application service already
-- supplies the artifact ID and confirmation time whose exact values are asserted by
-- the public integration tracer.
--
-- name: InsertVerificationArtifact :exec
--
insert into verification_artifacts(id, upload_intent_id, verification_session_id, kind, storage_key, content_type, size_bytes, etag, created_at)
values (sqlc.arg(verification_artifact_id), sqlc.arg(upload_intent_id), sqlc.arg(verification_session_id), sqlc.arg(verification_artifact_kind), sqlc.arg(verification_artifact_storage_key), sqlc.arg(verification_artifact_content_type), sqlc.arg(verification_artifact_size_bytes), sqlc.arg(verification_artifact_etag), sqlc.arg(verification_artifact_created_at));

-- name: AcquireArtifactConfirmLock :exec
SELECT pg_advisory_lock(sqlc.arg(lock_key)::bigint);

-- name: ReleaseArtifactConfirmLock :one
SELECT pg_advisory_unlock(sqlc.arg(lock_key)::bigint);

-- name: HasRequiredAcceptedArtifacts :one
SELECT
    COUNT(*) FILTER (WHERE kind = 'identity_document') = 1
    AND COUNT(*) FILTER (WHERE kind = 'biometric_capture') = 1
    AS is_accepted
FROM verification_artifacts
WHERE verification_session_id = sqlc.arg(session_id);

-- name: GetVerificationArtifactsBySessionId :many
select kind, storage_key, content_type, size_bytes, etag
from verification_artifacts
where verification_session_id = sqlc.arg(session_id) and kind in ('identity_document', 'biometric_capture')
order by case kind
  when 'identity_document' then 1
  when 'biometric_capture' then 2
end;
