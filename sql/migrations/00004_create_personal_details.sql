-- +goose Up
CREATE TABLE personal_details (
    verification_session_id uuid PRIMARY KEY
        REFERENCES verification_sessions (id),
    full_name text NOT NULL,
    date_of_birth date NOT NULL,
    identity_number text NOT NULL,
    address text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
