\set ON_ERROR_STOP on

BEGIN;

INSERT INTO verification_sessions DEFAULT VALUES
RETURNING id, status, created_at, updated_at;

SELECT id, status, created_at, updated_at
FROM verification_sessions
ORDER BY created_at DESC, id DESC
LIMIT 1;

ROLLBACK;
