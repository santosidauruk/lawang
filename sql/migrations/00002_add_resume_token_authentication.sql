-- +goose Up
ALTER TABLE verification_sessions
    ADD COLUMN resume_token_hash bytea,
    ADD COLUMN expires_at timestamptz;

-- Rows created by the Issue 001 SQL lab never had an issued raw token. Give them
-- unique, non-secret hashes and expire them immediately so they cannot be resumed.
UPDATE verification_sessions
SET resume_token_hash = sha256(convert_to(id::text || ':legacy-nonresumable', 'UTF8')),
    expires_at = created_at
WHERE resume_token_hash IS NULL OR expires_at IS NULL;

ALTER TABLE verification_sessions
    ALTER COLUMN resume_token_hash SET NOT NULL,
    ALTER COLUMN expires_at SET NOT NULL,
    ADD CONSTRAINT verification_sessions_resume_token_hash_length_check
        CHECK (octet_length(resume_token_hash) = 32),
    ADD CONSTRAINT verification_sessions_resume_token_hash_unique
        UNIQUE (resume_token_hash);
