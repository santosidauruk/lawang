\set ON_ERROR_STOP on

BEGIN;

-- Issue 010 Checkpoint 3 semantic fixture. Fixed timestamps make grace-boundary
-- mistakes visible by candidate ID instead of being hidden by relative now().
CREATE TEMP TABLE cleanup_proof_facts (
    case_name text PRIMARY KEY,
    session_id uuid NOT NULL UNIQUE,
    intent_id uuid NOT NULL UNIQUE,
    expected_candidate boolean NOT NULL,
    cleanup_now timestamptz NOT NULL
);

INSERT INTO cleanup_proof_facts (
    case_name, session_id, intent_id, expected_candidate, cleanup_now
)
VALUES
    ('pending_recently_overdue', 'c1000000-0000-4000-8000-000000000001', 'd1000000-0000-4000-8000-000000000001', false, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('superseded_older_than_grace', 'c1000000-0000-4000-8000-000000000002', 'd1000000-0000-4000-8000-000000000002', true, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('expired_exactly_at_boundary', 'c1000000-0000-4000-8000-000000000003', 'd1000000-0000-4000-8000-000000000003', false, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('expired_older_than_grace', 'c1000000-0000-4000-8000-000000000004', 'd1000000-0000-4000-8000-000000000004', true, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('validation_failed_older_than_grace', 'c1000000-0000-4000-8000-000000000005', 'd1000000-0000-4000-8000-000000000005', true, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('confirmed_older_than_grace', 'c1000000-0000-4000-8000-000000000006', 'd1000000-0000-4000-8000-000000000006', false, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('artifact_backed_eligible_status', 'c1000000-0000-4000-8000-000000000007', 'd1000000-0000-4000-8000-000000000007', false, TIMESTAMPTZ '2026-09-25 10:00:00+00'),
    ('already_deleted_eligible_status', 'c1000000-0000-4000-8000-000000000008', 'd1000000-0000-4000-8000-000000000008', false, TIMESTAMPTZ '2026-09-25 10:00:00+00');

INSERT INTO verification_sessions (
    id, resume_token_hash, status, created_at, updated_at, expires_at
)
SELECT
    session_id,
    sha256(convert_to('cleanup-proof-' || case_name, 'UTF8')),
    'created',
    cleanup_now - interval '48 hours',
    cleanup_now - interval '48 hours',
    cleanup_now + interval '30 minutes'
FROM cleanup_proof_facts;

INSERT INTO upload_intents (
    id,
    verification_session_id,
    kind,
    storage_key,
    status,
    created_at,
    latest_status_change_at,
    expires_at,
    confirmed_at,
    object_deleted_at,
    failure_code
)
SELECT
    intent_id,
    session_id,
    'identity_document',
    'verification-sessions/' || session_id::text || '/identity_document/' || intent_id::text,
    CASE case_name
        WHEN 'pending_recently_overdue' THEN 'pending'
        WHEN 'superseded_older_than_grace' THEN 'superseded'
        WHEN 'expired_exactly_at_boundary' THEN 'expired'
        WHEN 'expired_older_than_grace' THEN 'expired'
        WHEN 'validation_failed_older_than_grace' THEN 'validation_failed'
        WHEN 'confirmed_older_than_grace' THEN 'confirmed'
        WHEN 'artifact_backed_eligible_status' THEN 'expired'
        WHEN 'already_deleted_eligible_status' THEN 'superseded'
    END,
    cleanup_now - interval '48 hours',
    CASE case_name
        WHEN 'pending_recently_overdue' THEN cleanup_now - interval '1 hour'
        WHEN 'expired_exactly_at_boundary' THEN cleanup_now - interval '24 hours'
        ELSE cleanup_now - interval '25 hours'
    END,
    CASE case_name
        WHEN 'pending_recently_overdue' THEN cleanup_now - interval '1 hour'
        ELSE cleanup_now - interval '47 hours'
    END,
    CASE WHEN case_name = 'confirmed_older_than_grace'
        THEN cleanup_now - interval '25 hours'
        ELSE NULL
    END,
    CASE WHEN case_name = 'already_deleted_eligible_status'
        THEN cleanup_now - interval '1 hour'
        ELSE NULL
    END,
    CASE WHEN case_name = 'validation_failed_older_than_grace'
        THEN 'identity_number_mismatch'
        ELSE NULL
    END
FROM cleanup_proof_facts;

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
    'e1000000-0000-4000-8000-000000000007',
    intent_id,
    session_id,
    'identity_document',
    'verification-sessions/' || session_id::text || '/identity_document/' || intent_id::text,
    'image/jpeg',
    2048,
    'artifact-backed-proof-etag',
    cleanup_now - interval '25 hours'
FROM cleanup_proof_facts
WHERE case_name = 'artifact_backed_eligible_status';

DO $$
DECLARE
    materialized_count bigint;
BEGIN
    -- Production-equivalent MaterializeExpiredUploadIntents ACT. The logical
    -- transition time must remain the row's expires_at, not cleanup_now.
    UPDATE upload_intents
    SET
        status = 'expired',
        latest_status_change_at = expires_at
    WHERE expires_at <= TIMESTAMPTZ '2026-09-25 10:00:00+00'
      AND status = 'pending';

    GET DIAGNOSTICS materialized_count = ROW_COUNT;
    IF materialized_count <> 1 THEN
        RAISE EXCEPTION
            'materialized rows = %, expected exactly 1 overdue pending intent',
            materialized_count;
    END IF;
END
$$;

DO $$
DECLARE
    stored_status text;
    stored_change timestamptz;
    stored_expiry timestamptz;
BEGIN
    SELECT intent.status, intent.latest_status_change_at, intent.expires_at
    INTO stored_status, stored_change, stored_expiry
    FROM upload_intents AS intent
    JOIN cleanup_proof_facts AS fact ON fact.intent_id = intent.id
    WHERE fact.case_name = 'pending_recently_overdue';

    IF stored_status IS DISTINCT FROM 'expired' THEN
        RAISE EXCEPTION
            'materialized status = %, expected expired',
            stored_status;
    END IF;
    IF stored_change IS DISTINCT FROM stored_expiry THEN
        RAISE EXCEPTION
            'materialized latest_status_change_at = %, expected expires_at %',
            stored_change,
            stored_expiry;
    END IF;
END
$$;

-- Production-equivalent SelectStaleUploadIntents ACT. The limit is deliberately
-- larger than the fixture so this proof compares the complete candidate set.
CREATE TEMP TABLE actual_cleanup_candidates AS
SELECT intent.id AS intent_id, intent.storage_key
FROM upload_intents AS intent
WHERE intent.status IN ('superseded', 'expired', 'validation_failed')
  AND intent.object_deleted_at IS NULL
  AND intent.latest_status_change_at < TIMESTAMPTZ '2026-09-24 10:00:00+00'
  AND NOT EXISTS (
      SELECT 1
      FROM verification_artifacts AS artifact
      WHERE artifact.upload_intent_id = intent.id
  )
LIMIT 100;

CREATE TEMP TABLE expected_cleanup_candidates AS
SELECT
    fact.intent_id,
    'verification-sessions/' || fact.session_id::text
        || '/identity_document/' || fact.intent_id::text AS storage_key
FROM cleanup_proof_facts AS fact
WHERE fact.expected_candidate;

DO $$
DECLARE
    actual_count bigint;
BEGIN
    SELECT count(*) INTO actual_count FROM actual_cleanup_candidates;
    IF actual_count <> 3 THEN
        RAISE EXCEPTION
            'selected candidate count = %, expected 3',
            actual_count;
    END IF;

    IF EXISTS (
        SELECT intent_id, storage_key
        FROM expected_cleanup_candidates
        EXCEPT
        SELECT intent_id, storage_key
        FROM actual_cleanup_candidates
    ) THEN
        RAISE EXCEPTION 'expected cleanup candidate is missing from actual selection';
    END IF;

    IF EXISTS (
        SELECT intent_id, storage_key
        FROM actual_cleanup_candidates
        EXCEPT
        SELECT intent_id, storage_key
        FROM expected_cleanup_candidates
    ) THEN
        RAISE EXCEPTION 'unsafe or unexpected cleanup candidate was selected';
    END IF;
END
$$;

SELECT 'safe orphan cleanup proof passed' AS result;

ROLLBACK;
