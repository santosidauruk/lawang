\set ON_ERROR_STOP on

CREATE TEMP TABLE submission_readiness_success_fixture (
    session_id uuid PRIMARY KEY,
    identity_intent_id uuid NOT NULL UNIQUE,
    biometric_intent_id uuid NOT NULL UNIQUE,
    identity_storage_key text NOT NULL UNIQUE,
    biometric_storage_key text NOT NULL UNIQUE
);

WITH facts AS (
    SELECT
        gen_random_uuid() AS session_id,
        gen_random_uuid() AS identity_intent_id,
        gen_random_uuid() AS biometric_intent_id
)
INSERT INTO submission_readiness_success_fixture (
    session_id,
    identity_intent_id,
    biometric_intent_id,
    identity_storage_key,
    biometric_storage_key
)
SELECT
    session_id,
    identity_intent_id,
    biometric_intent_id,
    'verification-sessions/' || session_id::text
        || '/identity_document/' || identity_intent_id::text,
    'verification-sessions/' || session_id::text
        || '/biometric_capture/' || biometric_intent_id::text
FROM facts;

INSERT INTO verification_sessions (
    id,
    resume_token_hash,
    status,
    expires_at
)
SELECT
    session_id,
    sha256(convert_to(
        'submission-readiness-proof-' || session_id::text,
        'UTF8'
    )),
    'biometric_capture_uploaded',
    now() + interval '30 minutes'
FROM submission_readiness_success_fixture;

INSERT INTO upload_intents (
    id,
    verification_session_id,
    kind,
    storage_key,
    status,
    expires_at,
    confirmed_at
)
SELECT
    identity_intent_id,
    session_id,
    'identity_document',
    identity_storage_key,
    'confirmed',
    now() + interval '5 minutes',
    now()
FROM submission_readiness_success_fixture
UNION ALL
SELECT
    biometric_intent_id,
    session_id,
    'biometric_capture',
    biometric_storage_key,
    'confirmed',
    now() + interval '5 minutes',
    now()
FROM submission_readiness_success_fixture;

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
    gen_random_uuid(),
    identity_intent_id,
    session_id,
    'identity_document',
    identity_storage_key,
    'image/jpeg',
    2048,
    'submission-readiness-identity-etag',
    now()
FROM submission_readiness_success_fixture
UNION ALL
SELECT
    gen_random_uuid(),
    biometric_intent_id,
    session_id,
    'biometric_capture',
    biometric_storage_key,
    'image/jpeg',
    2048,
    'submission-readiness-biometric-etag',
    now()
FROM submission_readiness_success_fixture;

ANALYZE verification_artifacts;

EXPLAIN (COSTS OFF)
SELECT
    COUNT(*) FILTER (WHERE kind = 'identity_document') = 1
    AND COUNT(*) FILTER (WHERE kind = 'biometric_capture') = 1
FROM verification_artifacts
WHERE verification_session_id = (
    SELECT session_id
    FROM submission_readiness_success_fixture
);

DO $proof$
DECLARE
    target_session_id uuid;
    has_required_accepted_artifacts boolean;
BEGIN
    SELECT session_id
    INTO target_session_id
    FROM submission_readiness_success_fixture;

    SELECT
        COUNT(*) FILTER (WHERE kind = 'identity_document') = 1
        AND COUNT(*) FILTER (WHERE kind = 'biometric_capture') = 1
    INTO has_required_accepted_artifacts
    FROM verification_artifacts
    WHERE verification_session_id = target_session_id;

    IF has_required_accepted_artifacts IS DISTINCT FROM TRUE THEN
        RAISE EXCEPTION
            'same-session accepted artifact readiness = %, expected true',
            has_required_accepted_artifacts;
    END IF;
END
$proof$;

DO $proof$
BEGIN
    RAISE NOTICE
        'proof passed: same-session accepted artifact submission readiness';
END
$proof$;
