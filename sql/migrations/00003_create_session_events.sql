-- +goose Up
CREATE TABLE session_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES verification_sessions (id),
    event_type text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT session_events_type_check CHECK (
        event_type IN (
            'submit_personal_details',
            'confirm_identity_document',
            'confirm_biometric_capture',
            'submit_session',
            'verification_passed',
            'verification_failed',
            'expire'
        )
    ),
    CONSTRAINT session_events_safe_metadata_check CHECK (
        jsonb_typeof(metadata) = 'object'
        AND (metadata - 'outcome') = '{}'::jsonb
        AND (
            NOT metadata ? 'outcome'
            OR (
                jsonb_typeof(metadata -> 'outcome') = 'string'
                AND metadata ->> 'outcome' IN ('accepted', 'local_validation_failed')
            )
        )
    )
);

CREATE FUNCTION reject_session_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Session Events are append-only'
        USING ERRCODE = 'object_not_in_prerequisite_state';
END;
$$;

CREATE TRIGGER session_events_reject_mutation
BEFORE UPDATE OR DELETE ON session_events
FOR EACH ROW
EXECUTE FUNCTION reject_session_event_mutation();
