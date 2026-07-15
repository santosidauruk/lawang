-- +goose Up
CREATE TABLE verification_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status text NOT NULL DEFAULT 'created',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT verification_sessions_status_check CHECK (
        status IN (
            'created',
            'personal_details_submitted',
            'identity_document_uploaded',
            'biometric_capture_uploaded',
            'verification_pending',
            'verified',
            'rejected',
            'expired'
        )
    )
);
