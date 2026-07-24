\set ON_ERROR_STOP on

CREATE TEMP TABLE verification_artifact_proof_sessions (
    scenario text PRIMARY KEY,
    session_id uuid NOT NULL UNIQUE
);

CREATE TEMP TABLE verification_artifact_proof_intents (
    scenario text PRIMARY KEY,
    intent_id uuid NOT NULL UNIQUE,
    session_id uuid NOT NULL,
    kind text NOT NULL,
    storage_key text NOT NULL
);

INSERT INTO verification_artifact_proof_sessions (scenario, session_id)
SELECT
    scenario,
    gen_random_uuid()
FROM unnest(ARRAY[
    'primary',
    'secondary',
    'ownership',
    'metadata',
    'kind'
]) AS scenarios(scenario);

INSERT INTO verification_sessions (id, resume_token_hash, expires_at)
SELECT
    session_id,
    sha256(convert_to(
        'verification-artifact-proof-' || scenario || '-' || session_id::text,
        'UTF8'
    )),
    now() + interval '30 minutes'
FROM verification_artifact_proof_sessions;

INSERT INTO verification_artifact_proof_intents (
    scenario,
    intent_id,
    session_id,
    kind,
    storage_key
)
SELECT
    scenario,
    gen_random_uuid(),
    session_id,
    'identity_document',
    ''
FROM verification_artifact_proof_sessions
WHERE scenario IN ('primary', 'ownership', 'metadata', 'kind');

INSERT INTO verification_artifact_proof_intents (
    scenario,
    intent_id,
    session_id,
    kind,
    storage_key
)
SELECT
    'primary-second',
    gen_random_uuid(),
    session_id,
    'identity_document',
    ''
FROM verification_artifact_proof_sessions
WHERE scenario = 'primary';

UPDATE verification_artifact_proof_intents
SET storage_key = 'verification-sessions/'
    || session_id::text
    || '/'
    || kind
    || '/'
    || intent_id::text;

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
    intent_id,
    session_id,
    kind,
    storage_key,
    'confirmed',
    now() + interval '5 minutes',
    now()
FROM verification_artifact_proof_intents;

-- A complete accepted artifact persists the exact bounded values supplied by the
-- application.
WITH source AS (
    SELECT *
    FROM verification_artifact_proof_intents
    WHERE scenario = 'primary'
), inserted AS (
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
        intent_id,
        session_id,
        kind,
        storage_key,
        'image/jpeg',
        1024,
        'proof-etag',
        TIMESTAMPTZ '2026-07-23 10:00:00+00'
    FROM source
    RETURNING *
)
SELECT count(*) AS inserted_artifact_count
FROM inserted;

DO $proof$
DECLARE
    valid_count integer;
BEGIN
    SELECT count(*)
    INTO valid_count
    FROM verification_artifacts AS artifact
    JOIN verification_artifact_proof_intents AS intent
      ON intent.scenario = 'primary'
     AND artifact.upload_intent_id = intent.intent_id
     AND artifact.verification_session_id = intent.session_id
     AND artifact.kind = intent.kind
     AND artifact.storage_key = intent.storage_key
    WHERE artifact.content_type = 'image/jpeg'
      AND artifact.size_bytes = 1024
      AND artifact.etag = 'proof-etag'
      AND artifact.created_at = TIMESTAMPTZ '2026-07-23 10:00:00+00';

    IF valid_count <> 1 THEN
        RAISE EXCEPTION
            'valid Verification Artifact count = %, expected 1',
            valid_count;
    END IF;
END
$proof$;

-- The same Upload Intent cannot produce a second artifact. ON CONFLICT targets this
-- exact constraint because a literal duplicate also overlaps the session/kind unique
-- constraint, making the order of raised unique violations unsuitable as a proof.
DO $proof$
DECLARE
    inserted_count integer;
    constraint_count integer;
BEGIN
    SELECT count(*)
    INTO constraint_count
    FROM pg_constraint
    WHERE conrelid = 'verification_artifacts'::regclass
      AND conname = 'verification_artifacts_upload_intent_id_unique'
      AND contype = 'u';

    IF constraint_count <> 1 THEN
        RAISE EXCEPTION
            'Upload Intent unique constraint count = %, expected 1',
            constraint_count;
    END IF;

    WITH attempted AS (
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
            intent_id,
            session_id,
            kind,
            storage_key,
            'image/jpeg',
            1024,
            'duplicate-intent-etag',
            now()
        FROM verification_artifact_proof_intents
        WHERE scenario = 'primary'
        ON CONFLICT ON CONSTRAINT verification_artifacts_upload_intent_id_unique
        DO NOTHING
        RETURNING 1
    )
    SELECT count(*) INTO inserted_count FROM attempted;

    IF inserted_count <> 0 THEN
        RAISE EXCEPTION
            'database accepted a second artifact for one Upload Intent';
    END IF;
END
$proof$;

-- A different Upload Intent cannot create a second accepted artifact for the same
-- Verification Session and kind.
DO $proof$
DECLARE
    violated_constraint text;
BEGIN
    BEGIN
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
            intent_id,
            session_id,
            kind,
            storage_key,
            'image/jpeg',
            1024,
            'duplicate-session-kind-etag',
            now()
        FROM verification_artifact_proof_intents
        WHERE scenario = 'primary-second';

        RAISE EXCEPTION
            'database accepted a second artifact for one session/kind';
    EXCEPTION
        WHEN unique_violation THEN
            GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
            IF violated_constraint IS DISTINCT FROM
                'verification_artifacts_unique_session_kind'
            THEN
                RAISE EXCEPTION
                    'unexpected session/kind constraint %, expected verification_artifacts_unique_session_kind',
                    violated_constraint;
            END IF;
    END;
END
$proof$;

-- Unknown kinds are rejected before foreign-key ownership is considered.
DO $proof$
DECLARE
    violated_constraint text;
BEGIN
    BEGIN
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
            intent_id,
            session_id,
            'unknown_kind',
            storage_key,
            'image/jpeg',
            1024,
            'unknown-kind-etag',
            now()
        FROM verification_artifact_proof_intents
        WHERE scenario = 'kind';

        RAISE EXCEPTION 'database accepted an unknown artifact kind';
    EXCEPTION
        WHEN check_violation THEN
            GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
            IF violated_constraint IS DISTINCT FROM
                'verification_artifacts_kind_check'
            THEN
                RAISE EXCEPTION
                    'unexpected kind constraint %, expected verification_artifacts_kind_check',
                    violated_constraint;
            END IF;
    END;
END
$proof$;

-- Empty storage key, content type, and ETag share one bounded metadata-shape
-- constraint. Zero and negative sizes use the positive-size constraint.
DO $proof$
DECLARE
    violated_constraint text;
    metadata_case record;
BEGIN
    FOR metadata_case IN
        SELECT *
        FROM (VALUES
            ('empty_storage_key', '', 'image/jpeg', 1024::bigint, 'etag'),
            ('empty_content_type', NULL, '', 1024::bigint, 'etag'),
            ('empty_etag', NULL, 'image/jpeg', 1024::bigint, ''),
            ('zero_size', NULL, 'image/jpeg', 0::bigint, 'etag'),
            ('negative_size', NULL, 'image/jpeg', (-1)::bigint, 'etag')
        ) AS cases(
            scenario,
            overridden_storage_key,
            content_type,
            size_bytes,
            etag
        )
    LOOP
        BEGIN
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
                intent_id,
                session_id,
                kind,
                COALESCE(metadata_case.overridden_storage_key, storage_key),
                metadata_case.content_type,
                metadata_case.size_bytes,
                metadata_case.etag,
                now()
            FROM verification_artifact_proof_intents
            WHERE scenario = 'metadata';

            RAISE EXCEPTION
                'database accepted invalid metadata case %',
                metadata_case.scenario;
        EXCEPTION
            WHEN check_violation THEN
                GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
                IF metadata_case.scenario IN (
                    'empty_storage_key',
                    'empty_content_type',
                    'empty_etag'
                ) AND violated_constraint IS DISTINCT FROM
                    'verification_artifacts_metadata_not_empty_check'
                THEN
                    RAISE EXCEPTION
                        'metadata case % violated %, expected verification_artifacts_metadata_not_empty_check',
                        metadata_case.scenario,
                        violated_constraint;
                ELSIF metadata_case.scenario IN ('zero_size', 'negative_size')
                    AND violated_constraint IS DISTINCT FROM
                        'verification_artifacts_size_bytes_positive'
                THEN
                    RAISE EXCEPTION
                        'size case % violated %, expected verification_artifacts_size_bytes_positive',
                        metadata_case.scenario,
                        violated_constraint;
                END IF;
        END;
    END LOOP;
END
$proof$;

-- The direct Verification Session FK is intentionally redundant with the composite
-- ownership path, so its independent existence is verified through PostgreSQL's
-- catalog. An unknown Upload Intent isolates the composite ownership FK behavior.
DO $proof$
DECLARE
    direct_session_fk_count integer;
    violated_constraint text;
BEGIN
    SELECT count(*)
    INTO direct_session_fk_count
    FROM pg_constraint
    WHERE conrelid = 'verification_artifacts'::regclass
      AND conname = 'verification_artifacts_session_fk'
      AND contype = 'f';

    IF direct_session_fk_count <> 1 THEN
        RAISE EXCEPTION
            'direct Verification Session FK count = %, expected 1',
            direct_session_fk_count;
    END IF;

    BEGIN
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
            gen_random_uuid(),
            session_id,
            'identity_document',
            'unknown-intent-key',
            'image/jpeg',
            1024,
            'unknown-intent-etag',
            now()
        FROM verification_artifact_proof_sessions
        WHERE scenario = 'ownership';

        RAISE EXCEPTION 'database accepted an unknown Upload Intent';
    EXCEPTION
        WHEN foreign_key_violation THEN
            GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
            IF violated_constraint IS DISTINCT FROM
                'verification_artifacts_upload_intent_ownership_fk'
            THEN
                RAISE EXCEPTION
                    'unknown Upload Intent violated %, expected ownership FK',
                    violated_constraint;
            END IF;
    END;
END
$proof$;

-- A missing Verification Session is rejected. Both direct and composite foreign keys
-- reject this row, so either exact declared FK may be PostgreSQL's first violation.
DO $proof$
DECLARE
    violated_constraint text;
BEGIN
    BEGIN
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
            intent_id,
            gen_random_uuid(),
            kind,
            storage_key,
            'image/jpeg',
            1024,
            'unknown-session-etag',
            now()
        FROM verification_artifact_proof_intents
        WHERE scenario = 'ownership';

        RAISE EXCEPTION 'database accepted an unknown Verification Session';
    EXCEPTION
        WHEN foreign_key_violation THEN
            GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
            IF violated_constraint NOT IN (
                'verification_artifacts_session_fk',
                'verification_artifacts_upload_intent_ownership_fk'
            )
            THEN
                RAISE EXCEPTION
                    'unknown Verification Session violated unexpected constraint %',
                    violated_constraint;
            END IF;
    END;
END
$proof$;

-- Session, kind, and storage key must all belong to the referenced Upload Intent.
DO $proof$
DECLARE
    violated_constraint text;
    ownership_case record;
BEGIN
    FOR ownership_case IN
        SELECT *
        FROM (VALUES
            ('wrong_session', 'secondary', 'identity_document', NULL),
            ('wrong_kind', 'ownership', 'biometric_capture', NULL),
            ('wrong_storage_key', 'ownership', 'identity_document', 'different-key')
        ) AS cases(
            scenario,
            session_scenario,
            artifact_kind,
            overridden_storage_key
        )
    LOOP
        BEGIN
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
                intent.intent_id,
                artifact_session.session_id,
                ownership_case.artifact_kind,
                COALESCE(
                    ownership_case.overridden_storage_key,
                    intent.storage_key
                ),
                'image/jpeg',
                1024,
                'ownership-etag',
                now()
            FROM verification_artifact_proof_intents AS intent
            JOIN verification_artifact_proof_sessions AS artifact_session
              ON artifact_session.scenario = ownership_case.session_scenario
            WHERE intent.scenario = 'ownership';

            RAISE EXCEPTION
                'database accepted ownership mismatch %',
                ownership_case.scenario;
        EXCEPTION
            WHEN foreign_key_violation THEN
                GET STACKED DIAGNOSTICS violated_constraint = CONSTRAINT_NAME;
                IF violated_constraint IS DISTINCT FROM
                    'verification_artifacts_upload_intent_ownership_fk'
                THEN
                    RAISE EXCEPTION
                        'ownership case % violated %, expected ownership FK',
                        ownership_case.scenario,
                        violated_constraint;
                END IF;
        END;
    END LOOP;
END
$proof$;

DO $proof$
DECLARE
    final_artifact_count integer;
BEGIN
    SELECT count(*) INTO final_artifact_count
    FROM verification_artifacts;

    IF final_artifact_count <> 1 THEN
        RAISE EXCEPTION
            'proof left % Verification Artifacts, expected exactly 1 valid row',
            final_artifact_count;
    END IF;

    RAISE NOTICE 'proof passed: Verification Artifact constraints';
END
$proof$;
