\set ON_ERROR_STOP on

CREATE TEMP TABLE provider_verdict_proof_facts (
    case_name text PRIMARY KEY,
    session_id uuid NOT NULL,
    event_id uuid NOT NULL,
    payload bytea NOT NULL,
    processed_at timestamptz NOT NULL,
    rejection_reason text
);

INSERT INTO provider_verdict_proof_facts (
    case_name,
    session_id,
    event_id,
    payload,
    processed_at,
    rejection_reason
) VALUES
    (
        'verified',
        '571e2e21-6855-4d51-ad5a-91223c20ca76',
        '9e3982af-9467-4b25-b216-ceb93209be70',
        convert_to('{"eventId":"9e3982af-9467-4b25-b216-ceb93209be70","sessionId":"571e2e21-6855-4d51-ad5a-91223c20ca76","verdict":"verified"}', 'UTF8'),
        '2026-08-28T12:00:00Z',
        NULL
    ),
    (
        'rejected',
        '16198f98-f6d6-4984-86de-9ce32edb879a',
        'a2af5133-42a0-4ed7-85a9-f3d39df212a8',
        convert_to('{"eventId":"a2af5133-42a0-4ed7-85a9-f3d39df212a8","sessionId":"16198f98-f6d6-4984-86de-9ce32edb879a","verdict":"rejected","reason":"suspected_fraud"}', 'UTF8'),
        '2026-08-28T12:05:00Z',
        'suspected_fraud'
    );

INSERT INTO verification_sessions (
    id,
    resume_token_hash,
    status,
    expires_at,
    verification_deadline_at
)
SELECT
    session_id,
    CASE case_name
        WHEN 'verified' THEN decode(repeat('11', 32), 'hex')
        ELSE decode(repeat('22', 32), 'hex')
    END,
    'verification_pending',
    processed_at + interval '30 minutes',
    processed_at + interval '24 hours'
FROM provider_verdict_proof_facts;

BEGIN;

INSERT INTO webhook_events (id, reported_session_id, payload, received_at)
SELECT event_id, session_id, payload, processed_at
FROM provider_verdict_proof_facts
WHERE case_name = 'verified';

SELECT 1
FROM verification_sessions
WHERE id = (SELECT session_id FROM provider_verdict_proof_facts WHERE case_name = 'verified')
FOR UPDATE;

UPDATE verification_sessions
SET
    status = 'verified',
    verified_at = (SELECT processed_at FROM provider_verdict_proof_facts WHERE case_name = 'verified'),
    updated_at = (SELECT processed_at FROM provider_verdict_proof_facts WHERE case_name = 'verified')
WHERE id = (SELECT session_id FROM provider_verdict_proof_facts WHERE case_name = 'verified')
  AND status = 'verification_pending';

INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
SELECT session_id, 'verification_passed', '{}'::jsonb, processed_at
FROM provider_verdict_proof_facts
WHERE case_name = 'verified';

UPDATE webhook_events
SET processing_status = 'applied', processed_at = received_at, ignore_reason = NULL
WHERE id = (SELECT event_id FROM provider_verdict_proof_facts WHERE case_name = 'verified')
  AND processing_status IS NULL;

COMMIT;

BEGIN;

INSERT INTO webhook_events (id, reported_session_id, payload, received_at)
SELECT event_id, session_id, payload, processed_at
FROM provider_verdict_proof_facts
WHERE case_name = 'rejected';

SELECT 1
FROM verification_sessions
WHERE id = (SELECT session_id FROM provider_verdict_proof_facts WHERE case_name = 'rejected')
FOR UPDATE;

UPDATE verification_sessions
SET
    status = 'rejected',
    rejected_at = (SELECT processed_at FROM provider_verdict_proof_facts WHERE case_name = 'rejected'),
    rejection_reason = (SELECT rejection_reason FROM provider_verdict_proof_facts WHERE case_name = 'rejected'),
    updated_at = (SELECT processed_at FROM provider_verdict_proof_facts WHERE case_name = 'rejected')
WHERE id = (SELECT session_id FROM provider_verdict_proof_facts WHERE case_name = 'rejected')
  AND status = 'verification_pending';

INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
SELECT session_id, 'verification_failed', '{}'::jsonb, processed_at
FROM provider_verdict_proof_facts
WHERE case_name = 'rejected';

UPDATE webhook_events
SET processing_status = 'applied', processed_at = received_at, ignore_reason = NULL
WHERE id = (SELECT event_id FROM provider_verdict_proof_facts WHERE case_name = 'rejected')
  AND processing_status IS NULL;

COMMIT;

DO $proof$
DECLARE
    verified_exact_count bigint;
    rejected_exact_count bigint;
    webhook_exact_count bigint;
    event_exact_count bigint;
BEGIN
    SELECT count(*) INTO verified_exact_count
    FROM verification_sessions s
    JOIN provider_verdict_proof_facts f ON f.session_id = s.id
    WHERE f.case_name = 'verified'
      AND s.status = 'verified'
      AND s.verified_at = f.processed_at
      AND s.updated_at = f.processed_at
      AND s.rejected_at IS NULL
      AND s.rejection_reason IS NULL;

    SELECT count(*) INTO rejected_exact_count
    FROM verification_sessions s
    JOIN provider_verdict_proof_facts f ON f.session_id = s.id
    WHERE f.case_name = 'rejected'
      AND s.status = 'rejected'
      AND s.rejected_at = f.processed_at
      AND s.updated_at = f.processed_at
      AND s.verified_at IS NULL
      AND s.rejection_reason = f.rejection_reason;

    SELECT count(*) INTO webhook_exact_count
    FROM webhook_events w
    JOIN provider_verdict_proof_facts f ON f.event_id = w.id
    WHERE w.reported_session_id = f.session_id
      AND w.payload = f.payload
      AND w.received_at = f.processed_at
      AND w.processing_status = 'applied'
      AND w.processed_at = f.processed_at
      AND w.ignore_reason IS NULL;

    SELECT count(*) INTO event_exact_count
    FROM session_events e
    JOIN provider_verdict_proof_facts f ON f.session_id = e.session_id
    WHERE e.event_type = CASE f.case_name
            WHEN 'verified' THEN 'verification_passed'
            ELSE 'verification_failed'
          END
      AND e.metadata = '{}'::jsonb
      AND e.occurred_at = f.processed_at;

    IF verified_exact_count <> 1
        OR rejected_exact_count <> 1
        OR webhook_exact_count <> 2
        OR event_exact_count <> 2 THEN
        RAISE EXCEPTION
            'signed verdict proof mismatch: verified %, rejected %, webhooks %, events %',
            verified_exact_count,
            rejected_exact_count,
            webhook_exact_count,
            event_exact_count;
    END IF;

    RAISE NOTICE
        'proof passed: one verified and one rejected signed verdict outcome committed atomically';
END
$proof$;
