-- name: CreateVerificationSession :one
INSERT INTO verification_sessions (resume_token_hash, expires_at)
VALUES ($1, $2)
RETURNING id, status, resume_token_hash, expires_at, created_at, updated_at;

-- name: GetVerificationSessionByID :one
SELECT id, status, resume_token_hash, expires_at, created_at, updated_at
FROM verification_sessions
WHERE id = $1;

-- name: GetAuthorizedVerificationSession :one
SELECT id, status, resume_token_hash, expires_at, created_at, updated_at
FROM verification_sessions
WHERE id = $1 AND resume_token_hash = $2;

-- name: GuardVerificationSessionState :execrows
UPDATE verification_sessions
SET status = sqlc.arg(next_state), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = sqlc.arg(expected_state);

-- name: LockVerificationSessionByID :one
SELECT id, status, resume_token_hash, expires_at, created_at, updated_at, verification_deadline_at
FROM verification_sessions
WHERE id = $1
FOR UPDATE;

-- name: ExpireSessionByID :execrows
update verification_sessions
set status = 'expired', updated_at = sqlc.arg(updated_at)
where id = sqlc.arg(id) and status not in ('verified', 'rejected', 'expired') and status = sqlc.arg(current_state);

-- name: GetDueSessionIDs :many
select id
from verification_sessions
where
  ( status = 'verification_pending' and verification_deadline_at is not null and verification_deadline_at <= sqlc.arg(deadline) )
  or
  ( status in ( 'created', 'personal_details_submitted', 'identity_document_uploaded', 'biometric_capture_uploaded') and expires_at <= sqlc.arg(deadline) )
order by
  case
    when status = 'verification_pending' then verification_deadline_at
    else expires_at
  end asc,
  id asc
limit sqlc.arg(batch_size);
