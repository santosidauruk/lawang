-- +goose Up

-- CHECKPOINT 3 — USER-AUTHORED MIGRATION
--
-- Write the first Verification Artifact migration here. A Verification Artifact is
-- accepted evidence, not an Upload Intent and not stored object bytes.
--
-- Required columns revealed by the public confirmation tracer:
--   - application-supplied UUID primary key;
--   - Verification Session ID;
--   - Upload Intent ID;
--   - bounded kind;
--   - storage key;
--   - confirmed content type, byte size, and ETag;
--   - application-supplied creation timestamp.
--
-- Required database invariants:
--   1. both parent identifiers have foreign keys;
--   2. one Upload Intent creates at most one Verification Artifact;
--   3. one accepted artifact exists per (Verification Session, kind);
--   4. kind admits only identity_document and biometric_capture;
--   5. storage key, content type, and ETag are non-empty;
--   6. size is strictly positive.
--
-- Decide explicitly how the database prevents an artifact from pairing a session or
-- kind with an Upload Intent owned by another session/kind. If this requires a
-- supporting unique constraint on upload_intents, keep that constraint in this
-- forward migration and explain it in the proof.
--
-- Do not copy JPEG/PNG/PDF or 10 MiB rules into this table. Those mappings are owned
-- by application code. Do not store raw file bytes, raw identity numbers, object
-- URLs, resume tokens, or raw extraction output.
--
-- CONTRACT GAP TO REVIEW:
-- docs/plan-go.md mentions a minimized extraction result, but the current bounded
-- application type does not define such a field. Do not invent one in this migration.
-- Raise it during review after the constraints above are GREEN.
--
-- This repository uses forward-only migrations; do not add a Goose Down section.
alter table upload_intents
add constraint upload_intents_id_session_kind_storage_key_unique
UNIQUE (id, verification_session_id, kind, storage_key);

create table verification_artifacts (
  id uuid PRIMARY KEY,
  upload_intent_id uuid NOT NULL,
  verification_session_id uuid NOT NULL,
  kind text NOT NULL,
  storage_key text NOT NULL,
  content_type text NOT NULL,
  size_bytes bigint NOT NULL,
  etag text NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT verification_artifacts_upload_intent_id_unique UNIQUE (upload_intent_id),
  CONSTRAINT verification_artifacts_metadata_not_empty_check CHECK (
    storage_key <> ''
    AND
    content_type <> ''
    AND
    etag <> ''
  ),
  CONSTRAINT verification_artifacts_kind_check CHECK (
    kind IN ('identity_document', 'biometric_capture')
  ),
  CONSTRAINT verification_artifacts_size_bytes_positive CHECK (
    size_bytes > 0
  ),
  CONSTRAINT verification_artifacts_unique_session_kind UNIQUE(verification_session_id, kind),
  CONSTRAINT verification_artifacts_session_fk
    FOREIGN KEY (verification_session_id)
    REFERENCES verification_sessions(id),
  CONSTRAINT verification_artifacts_upload_intent_ownership_fk
    FOREIGN KEY (
        upload_intent_id,
        verification_session_id,
        kind,
        storage_key
    )
    REFERENCES upload_intents (
        id,
        verification_session_id,
        kind,
        storage_key
    )
);


