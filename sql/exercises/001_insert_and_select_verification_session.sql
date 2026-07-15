\set ON_ERROR_STOP on

BEGIN;

INSERT INTO verification_sessions (resume_token_hash, expires_at)
VALUES (sha256(convert_to('sql-exercise-nonsecret-token', 'UTF8')), now() + interval '30 minutes')
RETURNING id, status, created_at, updated_at;

SELECT id, status, created_at, updated_at
FROM verification_sessions
ORDER BY created_at DESC, id DESC
LIMIT 1;

ROLLBACK;
