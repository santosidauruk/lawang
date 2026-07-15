\set ON_ERROR_STOP on

BEGIN;

CREATE TEMP TABLE resume_proof AS
WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (sha256(convert_to('proof-token', 'UTF8')), now() + interval '30 minutes')
    RETURNING id, resume_token_hash
)
SELECT id, resume_token_hash FROM inserted;

DO $$
DECLARE
    proof_id uuid;
    authorized_count integer;
    wrong_hash_count integer;
    raw_token_columns integer;
BEGIN
    SELECT id INTO proof_id FROM resume_proof;

    SELECT count(*) INTO authorized_count
    FROM verification_sessions
    WHERE id = proof_id
      AND resume_token_hash = sha256(convert_to('proof-token', 'UTF8'));
    IF authorized_count <> 1 THEN
        RAISE EXCEPTION 'authorized lookup returned % rows, expected 1', authorized_count;
    END IF;

    SELECT count(*) INTO wrong_hash_count
    FROM verification_sessions
    WHERE id = proof_id
      AND resume_token_hash = sha256(convert_to('wrong-token', 'UTF8'));
    IF wrong_hash_count <> 0 THEN
        RAISE EXCEPTION 'wrong token hash authorized a session';
    END IF;

    SELECT count(*) INTO raw_token_columns
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'verification_sessions'
      AND column_name IN ('resume_token', 'raw_resume_token');
    IF raw_token_columns <> 0 THEN
        RAISE EXCEPTION 'raw resume-token column exists';
    END IF;

    BEGIN
        INSERT INTO verification_sessions (resume_token_hash, expires_at)
        SELECT resume_token_hash, now() + interval '30 minutes'
        FROM resume_proof;
        RAISE EXCEPTION 'duplicate resume-token hash was accepted';
    EXCEPTION
        WHEN unique_violation THEN
            NULL;
    END;
END
$$;

SELECT id, octet_length(resume_token_hash) AS stored_hash_bytes
FROM resume_proof;

ROLLBACK;
