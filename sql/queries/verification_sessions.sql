-- name: InsertVerificationSession :one
INSERT INTO verification_sessions DEFAULT VALUES
RETURNING id, status, created_at, updated_at;
