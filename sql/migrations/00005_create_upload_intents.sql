-- +goose Up

-- The application generates each Upload Intent UUID and complete storage key before
-- persistence. File constraints are code-owned by bounded kind and are not copied
-- into this relation. This migration is forward-only.
CREATE TABLE upload_intents (
    id uuid PRIMARY KEY,
    verification_session_id uuid NOT NULL
        REFERENCES verification_sessions (id),
    kind text NOT NULL,
    storage_key text NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    latest_status_change_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    confirmed_at timestamptz,
    object_deleted_at timestamptz,
    failure_code text,
    CONSTRAINT upload_intents_storage_key_unique UNIQUE (storage_key),
    CONSTRAINT upload_intents_storage_key_not_empty_check CHECK (
        storage_key <> ''
    ),
    CONSTRAINT upload_intents_kind_check CHECK (
        kind IN ('identity_document', 'biometric_capture')
    ),
    CONSTRAINT upload_intents_status_check CHECK (
        status IN (
            'pending',
            'confirmed',
            'superseded',
            'expired',
            'validation_failed'
        )
    ),
    CONSTRAINT upload_intents_failure_code_status_check CHECK (
        (
            status = 'validation_failed'
            AND failure_code IS NOT NULL
            AND failure_code IN ('identity_number_mismatch')
        )
        OR
        (
            status <> 'validation_failed'
            AND failure_code IS NULL
        )
    ),
    CONSTRAINT upload_intents_confirmed_at_status_check CHECK (
        (
            status = 'confirmed'
            AND confirmed_at IS NOT NULL
        )
        OR
        (
            status <> 'confirmed'
            AND confirmed_at IS NULL
        )
    ),
    CONSTRAINT upload_intents_object_deletion_status_check CHECK (
        object_deleted_at IS NULL
        OR status IN ('superseded', 'expired', 'validation_failed')
    )
);

CREATE UNIQUE INDEX upload_intents_one_pending_per_session_kind_idx
ON upload_intents (verification_session_id, kind)
WHERE status = 'pending';
