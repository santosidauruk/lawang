\set ON_ERROR_STOP on

CREATE TEMP TABLE provider_submission_proof_facts (
    case_name text PRIMARY KEY,
    session_id uuid NOT NULL UNIQUE,
    identity_intent_id uuid NOT NULL UNIQUE,
    identity_artifact_id uuid NOT NULL UNIQUE,
    biometric_intent_id uuid NOT NULL UNIQUE,
    biometric_artifact_id uuid NOT NULL UNIQUE,
    outbox_id uuid NOT NULL UNIQUE,
    submitted_at timestamptz NOT NULL,
    applicant_expires_at timestamptz NOT NULL
);

INSERT INTO provider_submission_proof_facts (
    case_name,
    session_id,
    identity_intent_id,
    identity_artifact_id,
    biometric_intent_id,
    biometric_artifact_id,
    outbox_id,
    submitted_at,
    applicant_expires_at
)
VALUES
    (
        'success',
        '44b95525-44b7-4fd6-abcb-d590a110ee68',
        'd61c55aa-5c2e-4d67-ac35-432b2dcd45ce',
        'de73ed02-4b4b-4bd6-8e42-2f0e7b96ea09',
        '491375c3-e6a3-4ab6-ab6f-5e45463912b3',
        'bb205266-0bbb-4e25-8573-f0f888d83c33',
        '948d96ec-e5c7-4174-be06-49057ba48583',
        TIMESTAMPTZ '2026-08-22 10:00:00+00',
        TIMESTAMPTZ '2026-08-22 10:30:00+00'
    ),
    (
        'rollback',
        '913b101c-63a5-47e8-b921-647241e6ac76',
        '7b7dfebd-357f-495a-8c43-818f582a6e49',
        '702e17d5-7b40-43ee-9830-b28596ff998b',
        '899b9738-cdfa-403f-9019-05a79d43ea02',
        '33713433-cec0-40e0-b158-217131e572e5',
        'd66a576a-c998-421d-9239-f3accd901173',
        TIMESTAMPTZ '2026-08-22 11:00:00+00',
        TIMESTAMPTZ '2026-08-22 11:30:00+00'
    );

-- Both cases start from a semantically complete biometric_capture_uploaded
-- history. Readiness must still be derived from accepted artifacts inside each
-- submission transaction; this final state is not used as a readiness proxy.
INSERT INTO verification_sessions (
    id,
    resume_token_hash,
    status,
    created_at,
    updated_at,
    expires_at
)
SELECT
    session_id,
    sha256(convert_to('provider-submission-proof-' || case_name, 'UTF8')),
    'biometric_capture_uploaded',
    submitted_at - interval '1 hour',
    submitted_at - interval '5 minutes',
    applicant_expires_at
FROM provider_submission_proof_facts;

INSERT INTO personal_details (
    verification_session_id,
    full_name,
    date_of_birth,
    identity_number,
    address,
    created_at
)
SELECT
    session_id,
    'Provider Submission ' || case_name,
    DATE '2000-01-01',
    CASE case_name
        WHEN 'success' THEN '3173000000000016'
        ELSE '3173000000000024'
    END,
    'proof fixture address',
    submitted_at - interval '45 minutes'
FROM provider_submission_proof_facts;

INSERT INTO upload_intents (
    id,
    verification_session_id,
    kind,
    storage_key,
    status,
    created_at,
    latest_status_change_at,
    expires_at,
    confirmed_at
)
SELECT
    identity_intent_id,
    session_id,
    'identity_document',
    'verification-sessions/' || session_id::text
        || '/identity_document/' || identity_intent_id::text,
    'confirmed',
    submitted_at - interval '40 minutes',
    submitted_at - interval '30 minutes',
    submitted_at + interval '5 minutes',
    submitted_at - interval '30 minutes'
FROM provider_submission_proof_facts
UNION ALL
SELECT
    biometric_intent_id,
    session_id,
    'biometric_capture',
    'verification-sessions/' || session_id::text
        || '/biometric_capture/' || biometric_intent_id::text,
    'confirmed',
    submitted_at - interval '15 minutes',
    submitted_at - interval '5 minutes',
    submitted_at + interval '10 minutes',
    submitted_at - interval '5 minutes'
FROM provider_submission_proof_facts;

INSERT INTO verification_artifacts (
    id,
    upload_intent_id,
    verification_session_id,
    kind,
    storage_key,
    content_type,
    size_bytes,
    etag,
    created_at
)
SELECT
    identity_artifact_id,
    identity_intent_id,
    session_id,
    'identity_document',
    'verification-sessions/' || session_id::text
        || '/identity_document/' || identity_intent_id::text,
    'image/jpeg',
    2048,
    'identity-proof-etag-' || case_name,
    submitted_at - interval '30 minutes'
FROM provider_submission_proof_facts
UNION ALL
SELECT
    biometric_artifact_id,
    biometric_intent_id,
    session_id,
    'biometric_capture',
    'verification-sessions/' || session_id::text
        || '/biometric_capture/' || biometric_intent_id::text,
    'image/jpeg',
    4096,
    'biometric-proof-etag-' || case_name,
    submitted_at - interval '5 minutes'
FROM provider_submission_proof_facts;

INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
SELECT
    session_id,
    'submit_personal_details',
    '{}'::jsonb,
    submitted_at - interval '45 minutes'
FROM provider_submission_proof_facts
UNION ALL
SELECT
    session_id,
    'confirm_identity_document',
    '{"outcome":"accepted"}'::jsonb,
    submitted_at - interval '30 minutes'
FROM provider_submission_proof_facts
UNION ALL
SELECT
    session_id,
    'confirm_biometric_capture',
    '{"outcome":"accepted"}'::jsonb,
    submitted_at - interval '5 minutes'
FROM provider_submission_proof_facts;

-- SUCCESS: lock, transaction-bound readiness, state/deadline update, event append,
-- and outbox insert commit together.
BEGIN;

DO $proof$
DECLARE
    target_session_id uuid;
    target_outbox_id uuid;
    transaction_time timestamptz;
    has_required_accepted_artifacts boolean;
    affected_rows bigint;
BEGIN
    SELECT session_id, outbox_id, submitted_at
    INTO target_session_id, target_outbox_id, transaction_time
    FROM provider_submission_proof_facts
    WHERE case_name = 'success';

    PERFORM 1
    FROM verification_sessions
    WHERE id = target_session_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'success fixture session was not lockable';
    END IF;

    SELECT
        COUNT(*) FILTER (WHERE kind = 'identity_document') = 1
        AND COUNT(*) FILTER (WHERE kind = 'biometric_capture') = 1
    INTO has_required_accepted_artifacts
    FROM verification_artifacts
    WHERE verification_session_id = target_session_id;
    IF has_required_accepted_artifacts IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION 'success fixture was not artifact-ready';
    END IF;

    UPDATE verification_sessions
    SET
        status = 'verification_pending',
        verification_deadline_at = transaction_time + interval '24 hours',
        updated_at = transaction_time
    WHERE id = target_session_id
      AND status = 'biometric_capture_uploaded';
    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'success state transition affected % rows, expected 1', affected_rows;
    END IF;

    INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
    VALUES (target_session_id, 'submit_session', '{}'::jsonb, transaction_time);

    INSERT INTO outbox (id, verification_session_id, task_type, payload)
    VALUES (
        target_outbox_id,
        target_session_id,
        'provider:submit',
        jsonb_build_object('sessionId', target_session_id::text)
    );
END
$proof$;

COMMIT;

DO $proof$
DECLARE
    facts provider_submission_proof_facts%ROWTYPE;
    stored_status text;
    stored_updated_at timestamptz;
    stored_deadline timestamptz;
    stored_applicant_expiry timestamptz;
    submit_event_count bigint;
    unsafe_submit_event_count bigint;
    matching_outbox_count bigint;
BEGIN
    SELECT * INTO facts
    FROM provider_submission_proof_facts
    WHERE case_name = 'success';

    SELECT status, updated_at, verification_deadline_at, expires_at
    INTO stored_status, stored_updated_at, stored_deadline, stored_applicant_expiry
    FROM verification_sessions
    WHERE id = facts.session_id;

    IF stored_status <> 'verification_pending'
        OR stored_updated_at <> facts.submitted_at
        OR stored_deadline <> facts.submitted_at + interval '24 hours'
        OR stored_applicant_expiry <> facts.applicant_expires_at THEN
        RAISE EXCEPTION
            'success session outcome was not exact: status %, updated %, deadline %, applicant expiry %',
            stored_status,
            stored_updated_at,
            stored_deadline,
            stored_applicant_expiry;
    END IF;

    SELECT
        COUNT(*),
        COUNT(*) FILTER (WHERE metadata <> '{}'::jsonb)
    INTO submit_event_count, unsafe_submit_event_count
    FROM session_events
    WHERE session_id = facts.session_id
      AND event_type = 'submit_session'
      AND occurred_at = facts.submitted_at;
    IF submit_event_count <> 1 OR unsafe_submit_event_count <> 0 THEN
        RAISE EXCEPTION
            'success submit event count/unsafe metadata = %/%',
            submit_event_count,
            unsafe_submit_event_count;
    END IF;

    SELECT COUNT(*)
    INTO matching_outbox_count
    FROM outbox
    WHERE id = facts.outbox_id
      AND verification_session_id = facts.session_id
      AND task_type = 'provider:submit'
      AND payload = jsonb_build_object('sessionId', facts.session_id::text)
      AND published_at IS NULL
      AND claim_token IS NULL
      AND claimed_until IS NULL
      AND attempt_count = 0
      AND last_error_code IS NULL;
    IF matching_outbox_count <> 1 THEN
        RAISE EXCEPTION 'success unpublished outbox count = %, expected 1', matching_outbox_count;
    END IF;
END
$proof$;

DO $proof$
BEGIN
    RAISE NOTICE
        'proof passed: pending state, exact deadline, submit event, and unpublished outbox committed atomically';
END
$proof$;

-- ROLLBACK: raise after all four writes. PostgreSQL rolls back the nested block as
-- one unit; the enclosing proof catches only the exact deliberate failure so the
-- script can assert the durable pre-submission state afterward.
DO $proof$
DECLARE
    facts provider_submission_proof_facts%ROWTYPE;
    has_required_accepted_artifacts boolean;
    affected_rows bigint;
BEGIN
    SELECT * INTO facts
    FROM provider_submission_proof_facts
    WHERE case_name = 'rollback';

    BEGIN
        PERFORM 1
        FROM verification_sessions
        WHERE id = facts.session_id
        FOR UPDATE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'rollback fixture session was not lockable';
        END IF;

        SELECT
            COUNT(*) FILTER (WHERE kind = 'identity_document') = 1
            AND COUNT(*) FILTER (WHERE kind = 'biometric_capture') = 1
        INTO has_required_accepted_artifacts
        FROM verification_artifacts
        WHERE verification_session_id = facts.session_id;
        IF has_required_accepted_artifacts IS DISTINCT FROM TRUE THEN
            RAISE EXCEPTION 'rollback fixture was not artifact-ready';
        END IF;

        UPDATE verification_sessions
        SET
            status = 'verification_pending',
            verification_deadline_at = facts.submitted_at + interval '24 hours',
            updated_at = facts.submitted_at
        WHERE id = facts.session_id
          AND status = 'biometric_capture_uploaded';
        GET DIAGNOSTICS affected_rows = ROW_COUNT;
        IF affected_rows <> 1 THEN
            RAISE EXCEPTION 'rollback state transition affected % rows, expected 1', affected_rows;
        END IF;

        INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
        VALUES (facts.session_id, 'submit_session', '{}'::jsonb, facts.submitted_at);

        INSERT INTO outbox (id, verification_session_id, task_type, payload)
        VALUES (
            facts.outbox_id,
            facts.session_id,
            'provider:submit',
            jsonb_build_object('sessionId', facts.session_id::text)
        );

        RAISE EXCEPTION USING
            ERRCODE = 'P0001',
            MESSAGE = 'forced provider submission rollback';
    EXCEPTION
        WHEN raise_exception THEN
            IF SQLERRM <> 'forced provider submission rollback' THEN
                RAISE;
            END IF;
    END;
END
$proof$;

DO $proof$
DECLARE
    facts provider_submission_proof_facts%ROWTYPE;
    stored_status text;
    stored_updated_at timestamptz;
    stored_deadline timestamptz;
    stored_applicant_expiry timestamptz;
    submit_event_count bigint;
    outbox_count bigint;
BEGIN
    SELECT * INTO facts
    FROM provider_submission_proof_facts
    WHERE case_name = 'rollback';

    SELECT status, updated_at, verification_deadline_at, expires_at
    INTO stored_status, stored_updated_at, stored_deadline, stored_applicant_expiry
    FROM verification_sessions
    WHERE id = facts.session_id;
    IF stored_status <> 'biometric_capture_uploaded'
        OR stored_updated_at <> facts.submitted_at - interval '5 minutes'
        OR stored_deadline IS NOT NULL
        OR stored_applicant_expiry <> facts.applicant_expires_at THEN
        RAISE EXCEPTION
            'rollback session changed: status %, updated %, deadline %, applicant expiry %',
            stored_status,
            stored_updated_at,
            stored_deadline,
            stored_applicant_expiry;
    END IF;

    SELECT COUNT(*)
    INTO submit_event_count
    FROM session_events
    WHERE session_id = facts.session_id
      AND event_type = 'submit_session';
    IF submit_event_count <> 0 THEN
        RAISE EXCEPTION 'rollback submit event count = %, expected 0', submit_event_count;
    END IF;

    SELECT COUNT(*)
    INTO outbox_count
    FROM outbox
    WHERE id = facts.outbox_id
       OR verification_session_id = facts.session_id;
    IF outbox_count <> 0 THEN
        RAISE EXCEPTION 'rollback outbox count = %, expected 0', outbox_count;
    END IF;
END
$proof$;

DO $proof$
BEGIN
    RAISE NOTICE
        'proof passed: forced failure rolled back pending state, deadline, submit event, and outbox';
END
$proof$;
