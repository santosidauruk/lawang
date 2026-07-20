\set ON_ERROR_STOP on

-- Checkpoint 1 (user-authored): replace this scaffold guard with an executable SQL
-- proof for the Upload Intent constraints introduced by migration 00005.
--
-- The proof must fail loudly when the database admits two pending intents for the
-- same Verification Session and evidence kind. It must also show that the chosen
-- constraint does not incorrectly forbid the historical statuses allowed by the
-- Issue 007 contract.
--
-- Actual competing-connection behavior belongs in
-- tests/integration/upload_intents_postgres_test.go; one psql connection does not
-- demonstrate a real concurrent race.

CREATE TEMP TABLE upload_intent_proof (
  scenario text PRIMARY KEY,
  session_id uuid NOT NULL
);

with inserted as (
  insert into verification_sessions (
    resume_token_hash,
    expires_at
  )
  values (
    sha256(
      convert_to('upload-intent-proof-' || gen_random_uuid()::text, 'UTF-8')
    ),
    now() + interval '30 minutes'
  )
  returning id
)

insert into upload_intent_proof(scenario, session_id)
select 'primary', id from inserted;

WITH candidate AS (
    SELECT
        gen_random_uuid() AS intent_id,
        session_id
    FROM upload_intent_proof
    WHERE scenario = 'primary'
)
INSERT INTO upload_intents (
    id,
    verification_session_id,
    kind,
    storage_key,
    expires_at
)
SELECT
    intent_id,
    session_id,
    'identity_document',
    'verification-sessions/'
        || session_id::text
        || '/identity_document/'
        || intent_id::text,
    now() + interval '5 minutes'
FROM candidate;

DO $proof$
DECLARE
    intent_count integer;
    stored_status text;
BEGIN
    SELECT count(*), min(status)
    INTO intent_count, stored_status
    FROM upload_intents
    WHERE verification_session_id = (
        SELECT session_id
        FROM upload_intent_proof
        WHERE scenario = 'primary'
    )
      AND kind = 'identity_document';

    IF intent_count <> 1 THEN
        RAISE EXCEPTION
            'valid insert produced % intents; expected 1',
            intent_count;
    END IF;

    IF stored_status IS DISTINCT FROM 'pending' THEN
        RAISE EXCEPTION
            'default status is %, expected pending',
            stored_status;
    END IF;
END
$proof$;

DO $proof$
DECLARE
    target_session_id uuid;
    second_intent_id uuid := gen_random_uuid();
    violated_constraint text;
BEGIN
    SELECT session_id
    INTO target_session_id
    FROM upload_intent_proof
    WHERE scenario = 'primary';

    BEGIN
        INSERT INTO upload_intents (
            id,
            verification_session_id,
            kind,
            storage_key,
            expires_at
        )
        VALUES (
            second_intent_id,
            target_session_id,
            'identity_document',
            'verification-sessions/'
                || target_session_id::text
                || '/identity_document/'
                || second_intent_id::text,
            now() + interval '5 minutes'
        );

        RAISE EXCEPTION
            'database accepted a second pending identity-document intent';
    EXCEPTION
        WHEN unique_violation THEN
            GET STACKED DIAGNOSTICS
                violated_constraint = CONSTRAINT_NAME;

            IF violated_constraint IS DISTINCT FROM
                'one_pending_one_kind_idx'
            THEN
                RAISE EXCEPTION
                    'unexpected unique constraint %, expected one_pending_one_kind_idx',
                    violated_constraint;
            END IF;
    END;
END
$proof$;
