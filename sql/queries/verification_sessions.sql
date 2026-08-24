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