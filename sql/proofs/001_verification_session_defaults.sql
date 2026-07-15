\set ON_ERROR_STOP on

BEGIN;

DO $proof$
DECLARE
    inserted verification_sessions%ROWTYPE;
BEGIN
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (sha256(convert_to('defaults-proof-token', 'UTF8')), now() + interval '30 minutes')
    RETURNING * INTO inserted;

    IF inserted.id IS NULL THEN
        RAISE EXCEPTION 'PostgreSQL did not generate a Verification Session UUID';
    END IF;
    IF inserted.status <> 'created' THEN
        RAISE EXCEPTION 'default status was %, expected created', inserted.status;
    END IF;
    IF inserted.created_at IS NULL OR inserted.updated_at IS NULL THEN
        RAISE EXCEPTION 'PostgreSQL did not generate Verification Session timestamps';
    END IF;

    BEGIN
        INSERT INTO verification_sessions (status, resume_token_hash, expires_at)
        VALUES (
            'unknown_public_state',
            sha256(convert_to('invalid-status-proof-token', 'UTF8')),
            now() + interval '30 minutes'
        );
        RAISE EXCEPTION 'bounded status constraint accepted an unknown public state';
    EXCEPTION
        WHEN check_violation THEN
            NULL;
    END;

    RAISE NOTICE 'proof passed: database UUID, created state, timestamps, and bounded status';
END
$proof$;

ROLLBACK;
