\set ON_ERROR_STOP on

CREATE TEMP TABLE personal_details_proof (
    scenario text PRIMARY KEY,
    session_id uuid NOT NULL
);

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (
        sha256(convert_to('personal-details-success-' || gen_random_uuid()::text, 'UTF8')),
        now() + interval '30 minutes'
    )
    RETURNING id
)
INSERT INTO personal_details_proof (scenario, session_id)
SELECT 'success', id FROM inserted;

BEGIN;

INSERT INTO personal_details (
    verification_session_id, full_name, date_of_birth, identity_number, address
)
SELECT session_id, '', DATE '2000-02-29', '', ''
FROM personal_details_proof
WHERE scenario = 'success';

UPDATE verification_sessions
SET status = 'personal_details_submitted', updated_at = clock_timestamp()
WHERE id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'success')
  AND status = 'created';

INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
SELECT session_id, 'submit_personal_details', '{}'::jsonb, clock_timestamp()
FROM personal_details_proof
WHERE scenario = 'success';

COMMIT;

DO $proof$
DECLARE
    details_count integer;
    state_count integer;
    event_count integer;
BEGIN
    SELECT count(*) INTO details_count
    FROM personal_details
    WHERE verification_session_id = (
        SELECT session_id FROM personal_details_proof WHERE scenario = 'success'
    )
      AND full_name = ''
      AND date_of_birth = DATE '2000-02-29'
      AND identity_number = ''
      AND address = '';

    SELECT count(*) INTO state_count
    FROM verification_sessions
    WHERE id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'success')
      AND status = 'personal_details_submitted';

    SELECT count(*) INTO event_count
    FROM session_events
    WHERE session_id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'success')
      AND event_type = 'submit_personal_details'
      AND metadata = '{}'::jsonb;

    IF details_count <> 1 OR state_count <> 1 OR event_count <> 1 THEN
        RAISE EXCEPTION 'success transaction committed details %, state %, events %; expected 1 each',
            details_count, state_count, event_count;
    END IF;

    BEGIN
        INSERT INTO personal_details (
            verification_session_id, full_name, date_of_birth, identity_number, address
        )
        SELECT session_id, 'Different', DATE '1990-01-02', 'different', 'different'
        FROM personal_details_proof
        WHERE scenario = 'success';
        RAISE EXCEPTION 'one-to-one Personal Details constraint accepted a second row';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
END
$proof$;

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (
        sha256(convert_to('personal-details-rollback-' || gen_random_uuid()::text, 'UTF8')),
        now() + interval '30 minutes'
    )
    RETURNING id
)
INSERT INTO personal_details_proof (scenario, session_id)
SELECT 'rollback', id FROM inserted;

BEGIN;

INSERT INTO personal_details (
    verification_session_id, full_name, date_of_birth, identity_number, address
)
SELECT session_id, 'Rollback', DATE '1990-01-02', 'rollback', 'rollback'
FROM personal_details_proof
WHERE scenario = 'rollback';

UPDATE verification_sessions
SET status = 'personal_details_submitted', updated_at = clock_timestamp()
WHERE id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'rollback')
  AND status = 'created';

INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
SELECT session_id, 'submit_personal_details', '{}'::jsonb, clock_timestamp()
FROM personal_details_proof
WHERE scenario = 'rollback';

-- Forced application failure after all three writes.
ROLLBACK;

DO $proof$
DECLARE
    details_count integer;
    state_count integer;
    event_count integer;
BEGIN
    SELECT count(*) INTO details_count
    FROM personal_details
    WHERE verification_session_id = (
        SELECT session_id FROM personal_details_proof WHERE scenario = 'rollback'
    );

    SELECT count(*) INTO state_count
    FROM verification_sessions
    WHERE id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'rollback')
      AND status = 'created';

    SELECT count(*) INTO event_count
    FROM session_events
    WHERE session_id = (SELECT session_id FROM personal_details_proof WHERE scenario = 'rollback');

    IF details_count <> 0 OR state_count <> 1 OR event_count <> 0 THEN
        RAISE EXCEPTION 'rollback left details %, created state %, events %; expected 0, 1, 0',
            details_count, state_count, event_count;
    END IF;

    RAISE NOTICE 'proof passed: one-to-one details and atomic details/state/event commit plus rollback';
END
$proof$;
