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
