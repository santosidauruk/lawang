\set ON_ERROR_STOP on

CREATE TEMP TABLE atomic_session_event_proof (
    scenario text PRIMARY KEY,
    session_id uuid NOT NULL
);

BEGIN;

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (
        sha256(convert_to('atomic-success-' || gen_random_uuid()::text, 'UTF8')),
        now() + interval '30 minutes'
    )
    RETURNING id
)
INSERT INTO atomic_session_event_proof (scenario, session_id)
SELECT 'success', id FROM inserted;

UPDATE verification_sessions
SET status = 'personal_details_submitted', updated_at = clock_timestamp()
WHERE id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'success')
  AND status = 'created';

INSERT INTO session_events (session_id, event_type, occurred_at)
SELECT session_id, 'submit_personal_details', clock_timestamp()
FROM atomic_session_event_proof
WHERE scenario = 'success';

COMMIT;

DO $proof$
DECLARE
    state_count integer;
    event_count integer;
BEGIN
    SELECT count(*) INTO state_count
    FROM verification_sessions
    WHERE id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'success')
      AND status = 'personal_details_submitted';

    SELECT count(*) INTO event_count
    FROM session_events
    WHERE session_id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'success')
      AND event_type = 'submit_personal_details';

    IF state_count <> 1 OR event_count <> 1 THEN
        RAISE EXCEPTION 'success transaction committed state rows % and event rows %, expected 1 and 1',
            state_count, event_count;
    END IF;
END
$proof$;

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (
        sha256(convert_to('atomic-rollback-' || gen_random_uuid()::text, 'UTF8')),
        now() + interval '30 minutes'
    )
    RETURNING id
)
INSERT INTO atomic_session_event_proof (scenario, session_id)
SELECT 'rollback', id FROM inserted;

BEGIN;

UPDATE verification_sessions
SET status = 'personal_details_submitted', updated_at = clock_timestamp()
WHERE id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'rollback')
  AND status = 'created';

-- Forced failure point: application returns an error here, before event append/commit.
ROLLBACK;

DO $proof$
DECLARE
    state_count integer;
    event_count integer;
BEGIN
    SELECT count(*) INTO state_count
    FROM verification_sessions
    WHERE id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'rollback')
      AND status = 'created';

    SELECT count(*) INTO event_count
    FROM session_events
    WHERE session_id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'rollback');

    IF state_count <> 1 OR event_count <> 0 THEN
        RAISE EXCEPTION 'rollback left created rows % and event rows %, expected 1 and 0',
            state_count, event_count;
    END IF;
END
$proof$;

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES (
        sha256(convert_to('event-order-' || gen_random_uuid()::text, 'UTF8')),
        now() + interval '30 minutes'
    )
    RETURNING id
)
INSERT INTO atomic_session_event_proof (scenario, session_id)
SELECT 'ordering', id FROM inserted;

INSERT INTO session_events (id, session_id, event_type, occurred_at)
SELECT '00000000-0000-0000-0000-000000000002', session_id, 'confirm_identity_document',
       '2026-07-15T10:00:00Z'::timestamptz
FROM atomic_session_event_proof WHERE scenario = 'ordering';

INSERT INTO session_events (id, session_id, event_type, occurred_at)
SELECT '00000000-0000-0000-0000-000000000001', session_id, 'submit_personal_details',
       '2026-07-15T10:00:00Z'::timestamptz
FROM atomic_session_event_proof WHERE scenario = 'ordering';

DO $proof$
DECLARE
    ordered_ids uuid[];
    protected_event_id uuid;
BEGIN
    SELECT array_agg(id ORDER BY occurred_at, id) INTO ordered_ids
    FROM session_events
    WHERE session_id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'ordering');

    IF ordered_ids <> ARRAY[
        '00000000-0000-0000-0000-000000000001'::uuid,
        '00000000-0000-0000-0000-000000000002'::uuid
    ] THEN
        RAISE EXCEPTION 'event ordering was %, expected occurrence time then stable event ID', ordered_ids;
    END IF;

    BEGIN
        INSERT INTO session_events (session_id, event_type)
        SELECT session_id, 'unknown' FROM atomic_session_event_proof WHERE scenario = 'ordering';
        RAISE EXCEPTION 'unknown event type was accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    BEGIN
        INSERT INTO session_events (session_id, event_type)
        SELECT session_id, 'verified' FROM atomic_session_event_proof WHERE scenario = 'ordering';
        RAISE EXCEPTION 'public state/result string was accepted as an event type';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    BEGIN
        INSERT INTO session_events (session_id, event_type, metadata)
        SELECT session_id, 'confirm_identity_document', '{"resume_token":"secret"}'::jsonb
        FROM atomic_session_event_proof WHERE scenario = 'ordering';
        RAISE EXCEPTION 'sensitive metadata was accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;

    SELECT id INTO protected_event_id
    FROM session_events
    WHERE session_id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'ordering')
    ORDER BY occurred_at, id
    LIMIT 1;

    BEGIN
        UPDATE session_events SET metadata = '{}'::jsonb WHERE id = protected_event_id;
        RAISE EXCEPTION 'Session Event update was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN NULL;
    END;

    BEGIN
        DELETE FROM session_events WHERE id = protected_event_id;
        RAISE EXCEPTION 'Session Event delete was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN NULL;
    END;

    RAISE NOTICE 'proof passed: atomic commit, rollback, bounded verbs/metadata, ordering, and append-only rows';
END
$proof$;

SELECT event_type, metadata, occurred_at, id
FROM session_events
WHERE session_id = (SELECT session_id FROM atomic_session_event_proof WHERE scenario = 'ordering')
ORDER BY occurred_at, id;
