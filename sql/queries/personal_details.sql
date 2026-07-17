-- name: LockVerificationSessionForPersonalDetails :one
SELECT id, status, resume_token_hash, expires_at, created_at, updated_at
FROM verification_sessions
WHERE id = $1
FOR UPDATE;

-- name: GetPersonalDetailsBySessionID :one
SELECT verification_session_id, full_name, date_of_birth, identity_number, address, created_at
FROM personal_details
WHERE verification_session_id = $1;

-- name: InsertPersonalDetails :exec
INSERT INTO personal_details (
    verification_session_id,
    full_name,
    date_of_birth,
    identity_number,
    address,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6);
