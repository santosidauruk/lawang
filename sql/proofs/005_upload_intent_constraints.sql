\set ON_ERROR_STOP on

CREATE TEMP TABLE upload_intent_proof_sessions (
    scenario text PRIMARY KEY,
    session_id uuid NOT NULL
);

CREATE TEMP TABLE upload_intent_proof_intents (
    scenario text PRIMARY KEY,
    intent_id uuid NOT NULL
);

WITH inserted AS (
    INSERT INTO verification_sessions (resume_token_hash, expires_at)
    VALUES
        (
            sha256(convert_to('upload-intent-primary-' || gen_random_uuid()::text, 'UTF8')),
            now() + interval '30 minutes'
        ),
        (
            sha256(convert_to('upload-intent-secondary-' || gen_random_uuid()::text, 'UTF8')),
            now() + interval '30 minutes'
        )
    RETURNING id
), numbered AS (
    SELECT id, row_number() OVER (ORDER BY id) AS position
    FROM inserted
)
INSERT INTO upload_intent_proof_sessions (scenario, session_id)
SELECT CASE position WHEN 1 THEN 'primary' ELSE 'secondary' END, id
FROM numbered;

-- A valid application-supplied ID/key receives the pending default.
WITH candidate AS (
    SELECT gen_random_uuid() AS intent_id, session_id
    FROM upload_intent_proof_sessions
    WHERE scenario = 'primary'
), inserted AS (
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
        'verification-sessions/' || session_id::text
            || '/identity_document/' || intent_id::text,
        now() + interval '5 minutes'
    FROM candidate
    RETURNING id
)
INSERT INTO upload_intent_proof_intents (scenario, intent_id)
SELECT 'initial_pending', id
FROM inserted;

DO $proof$
DECLARE
    intent_count integer;
    stored_status text;
BEGIN
    SELECT count(*), min(status)
    INTO intent_count, stored_status
    FROM upload_intents
    WHERE id = (
        SELECT intent_id
        FROM upload_intent_proof_intents
        WHERE scenario = 'initial_pending'
    );

    IF intent_count <> 1 OR stored_status IS DISTINCT FROM 'pending' THEN
        RAISE EXCEPTION
            'valid intent count/status = %/%, expected 1/pending',
            intent_count,
            stored_status;
    END IF;
END
$proof$;

-- A second pending row for the same session and kind is rejected by the partial index.
DO $proof$
DECLARE
    target_session_id uuid;
    candidate_id uuid := gen_random_uuid();
    violated_constraint text;
BEGIN
    SELECT session_id
    INTO target_session_id
    FROM upload_intent_proof_sessions
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
            candidate_id,
            target_session_id,
            'identity_document',
            'verification-sessions/' || target_session_id::text
                || '/identity_document/' || candidate_id::text,
            now() + interval '5 minutes'
        );
        RAISE EXCEPTION 'database accepted a second pending intent';
    EXCEPTION
        WHEN unique_violation THEN
            GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
            IF violated_constraint IS DISTINCT FROM
                'upload_intents_one_pending_per_session_kind_idx'
            THEN
                RAISE EXCEPTION
                    'unexpected constraint %, expected pending partial index',
                    violated_constraint;
            END IF;
    END;
END
$proof$;

-- Historical rows and independent session/kind pairs remain representable.
DO $proof$
DECLARE
    primary_session_id uuid;
    secondary_session_id uuid;
    candidate_id uuid;
    historical_count integer;
BEGIN
    SELECT session_id INTO primary_session_id
    FROM upload_intent_proof_sessions WHERE scenario = 'primary';
    SELECT session_id INTO secondary_session_id
    FROM upload_intent_proof_sessions WHERE scenario = 'secondary';

    FOR counter IN 1..2 LOOP
        candidate_id := gen_random_uuid();
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key, status, expires_at
        ) VALUES (
            candidate_id,
            primary_session_id,
            'identity_document',
            'verification-sessions/' || primary_session_id::text
                || '/identity_document/' || candidate_id::text,
            'superseded',
            now() - interval '1 minute'
        );
    END LOOP;

    candidate_id := gen_random_uuid();
    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, status,
        expires_at, object_deleted_at
    ) VALUES (
        candidate_id,
        primary_session_id,
        'identity_document',
        'verification-sessions/' || primary_session_id::text
            || '/identity_document/' || candidate_id::text,
        'expired',
        now() - interval '1 minute',
        now()
    );

    candidate_id := gen_random_uuid();
    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, status,
        expires_at, failure_code, object_deleted_at
    ) VALUES (
        candidate_id,
        primary_session_id,
        'identity_document',
        'verification-sessions/' || primary_session_id::text
            || '/identity_document/' || candidate_id::text,
        'validation_failed',
        now() - interval '1 minute',
        'identity_number_mismatch',
        now()
    );

    candidate_id := gen_random_uuid();
    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, status,
        expires_at, confirmed_at
    ) VALUES (
        candidate_id,
        primary_session_id,
        'identity_document',
        'verification-sessions/' || primary_session_id::text
            || '/identity_document/' || candidate_id::text,
        'confirmed',
        now() + interval '5 minutes',
        now()
    );

    candidate_id := gen_random_uuid();
    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, expires_at
    ) VALUES (
        candidate_id,
        primary_session_id,
        'biometric_capture',
        'verification-sessions/' || primary_session_id::text
            || '/biometric_capture/' || candidate_id::text,
        now() + interval '5 minutes'
    );

    candidate_id := gen_random_uuid();
    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, expires_at
    ) VALUES (
        candidate_id,
        secondary_session_id,
        'identity_document',
        'verification-sessions/' || secondary_session_id::text
            || '/identity_document/' || candidate_id::text,
        now() + interval '5 minutes'
    );

    SELECT count(*) INTO historical_count
    FROM upload_intents
    WHERE verification_session_id = primary_session_id
      AND kind = 'identity_document'
      AND status <> 'pending';

    IF historical_count <> 5 THEN
        RAISE EXCEPTION
            'historical intent count is %, expected 5',
            historical_count;
    END IF;
END
$proof$;

-- Invalid field/status combinations fail through their specific constraints.
DO $proof$
DECLARE
    target_session_id uuid;
    existing_storage_key text;
    candidate_id uuid;
    violated_constraint text;
BEGIN
    SELECT session_id INTO target_session_id
    FROM upload_intent_proof_sessions WHERE scenario = 'primary';
    SELECT storage_key INTO existing_storage_key
    FROM upload_intents
    WHERE id = (
        SELECT intent_id FROM upload_intent_proof_intents
        WHERE scenario = 'initial_pending'
    );

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key, status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'unknown_kind',
            'proof/' || candidate_id::text, 'superseded', now()
        );
        RAISE EXCEPTION 'database accepted an unknown kind';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM 'upload_intents_kind_check' THEN
            RAISE EXCEPTION 'unknown kind violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key, status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text, 'unknown_status', now()
        );
        RAISE EXCEPTION 'database accepted an unknown status';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM 'upload_intents_status_check' THEN
            RAISE EXCEPTION 'unknown status violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key, status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document', '',
            'superseded', now()
        );
        RAISE EXCEPTION 'database accepted an empty storage key';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_storage_key_not_empty_check'
        THEN
            RAISE EXCEPTION 'empty key violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key, status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            existing_storage_key, 'superseded', now()
        );
        RAISE EXCEPTION 'database accepted a duplicate storage key';
    EXCEPTION WHEN unique_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM 'upload_intents_storage_key_unique' THEN
            RAISE EXCEPTION 'duplicate key violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text,
            'validation_failed', now()
        );
        RAISE EXCEPTION 'validation_failed accepted a NULL failure code';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_failure_code_status_check'
        THEN
            RAISE EXCEPTION 'missing failure code violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at, failure_code
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text,
            'superseded', now(), 'identity_number_mismatch'
        );
        RAISE EXCEPTION 'non-failed status accepted a failure code';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_failure_code_status_check'
        THEN
            RAISE EXCEPTION 'unexpected failure/status constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text,
            'confirmed', now()
        );
        RAISE EXCEPTION 'confirmed status accepted a NULL confirmed_at';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_confirmed_at_status_check'
        THEN
            RAISE EXCEPTION 'missing confirmed_at violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at, confirmed_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text,
            'superseded', now(), now()
        );
        RAISE EXCEPTION 'non-confirmed status accepted confirmed_at';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_confirmed_at_status_check'
        THEN
            RAISE EXCEPTION 'unexpected confirmed_at/status constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at, confirmed_at, object_deleted_at
        ) VALUES (
            candidate_id, target_session_id, 'identity_document',
            'proof/' || candidate_id::text,
            'confirmed', now(), now(), now()
        );
        RAISE EXCEPTION 'confirmed intent described a deleted accepted object';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_object_deletion_status_check'
        THEN
            RAISE EXCEPTION 'confirmed deletion violated unexpected constraint %', violated_constraint;
        END IF;
    END;

    candidate_id := gen_random_uuid();
    BEGIN
        INSERT INTO upload_intents (
            id, verification_session_id, kind, storage_key,
            status, expires_at, object_deleted_at
        ) VALUES (
            candidate_id, target_session_id, 'biometric_capture',
            'proof/' || candidate_id::text,
            'pending', now(), now()
        );
        RAISE EXCEPTION 'pending intent described a deleted object';
    EXCEPTION WHEN check_violation THEN
        GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
        IF violated_constraint IS DISTINCT FROM
            'upload_intents_object_deletion_status_check'
        THEN
            RAISE EXCEPTION 'pending deletion violated unexpected constraint %', violated_constraint;
        END IF;
    END;
END
$proof$;

-- Supersede and replacement preserve history and leave one pending row.
DO $proof$
DECLARE
    target_session_id uuid;
    original_intent_id uuid;
    replacement_intent_id uuid := gen_random_uuid();
    transitioned_at timestamptz := TIMESTAMPTZ '2026-07-20 10:00:00+00';
    pending_count integer;
    recorded_change timestamptz;
BEGIN
    SELECT session_id INTO target_session_id
    FROM upload_intent_proof_sessions WHERE scenario = 'primary';
    SELECT intent_id INTO original_intent_id
    FROM upload_intent_proof_intents WHERE scenario = 'initial_pending';

    UPDATE upload_intents
    SET
        status = 'superseded',
        latest_status_change_at = transitioned_at
    WHERE id = original_intent_id
      AND status = 'pending';

    INSERT INTO upload_intents (
        id, verification_session_id, kind, storage_key, expires_at
    ) VALUES (
        replacement_intent_id,
        target_session_id,
        'identity_document',
        'verification-sessions/' || target_session_id::text
            || '/identity_document/' || replacement_intent_id::text,
        now() + interval '5 minutes'
    );

    SELECT count(*) INTO pending_count
    FROM upload_intents
    WHERE verification_session_id = target_session_id
      AND kind = 'identity_document'
      AND status = 'pending';

    SELECT latest_status_change_at INTO recorded_change
    FROM upload_intents
    WHERE id = original_intent_id;

    IF pending_count <> 1 THEN
        RAISE EXCEPTION
            'replacement left % pending identity intents, expected 1',
            pending_count;
    END IF;
    IF recorded_change IS DISTINCT FROM transitioned_at THEN
        RAISE EXCEPTION
            'status change timestamp is %, expected %',
            recorded_change,
            transitioned_at;
    END IF;

    RAISE NOTICE
        'proof passed: bounded Upload Intent schema, history, replacement, and one pending row';
END
$proof$;
