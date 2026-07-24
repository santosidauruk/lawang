-- CHECKPOINT 3 STEP 4 — USER-AUTHORED SUCCESS-PATH QUERY
--
-- Write exactly one query for the accepted confirmation tracer:
--
-- name: InsertVerificationArtifact :exec
--
insert into verification_artifacts(id, upload_intent_id, verification_session_id, kind, storage_key, content_type, size_bytes, etag, created_at)
values (sqlc.arg(verification_artifact_id), sqlc.arg(upload_intent_id), sqlc.arg(verification_session_id), sqlc.arg(verification_artifact_kind), sqlc.arg(verification_artifact_storage_key), sqlc.arg(verification_artifact_content_type), sqlc.arg(verification_artifact_size_bytes), sqlc.arg(verification_artifact_etag), sqlc.arg(verification_artifact_created_at));
-- Insert every field of artifact.VerificationArtifact explicitly:
--   id, upload_intent_id, verification_session_id, kind, storage_key,
--   content_type, size_bytes, etag, created_at.
--
-- Do not use database-generated ID/time values. The application service already
-- supplies the artifact ID and confirmation time whose exact values are asserted by
-- the public integration tracer.
