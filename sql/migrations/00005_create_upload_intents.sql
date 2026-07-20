-- +goose Up

-- Checkpoint 1 (user-authored): define the Upload Intent relation here.
--
-- Prove the domain contract from docs/issues/007-confirm-identity-document-uploads.md;
-- do not copy a finished schema from the agent. In particular, explain through the
-- schema how one pending intent per (Verification Session, evidence kind) is
-- enforced while historical confirmed, superseded, expired, or validation-failed
-- intents remain representable.
--
-- Keep this migration forward-only. Do not add a Goose Down section.

-- REVIEW CHECKPOINT 1 (blocking): revise the migration yourself before writing the
-- proof. The current statement is not executable and does not yet enforce the Issue
-- 007 invariants. Address each item below; do not remove a comment until you can
-- explain the corresponding decision.
--
-- 1. SQL syntax: a table column list needs a comma between `id` and
--    `verification_session_id`, and the CREATE TABLE statement should be terminated.
--
-- 2. Required ownership: `verification_session_id` currently accepts NULL. An Upload
--    Intent cannot exist without its owning Verification Session, so decide how the
--    schema makes that relationship mandatory. Keep the existing project's default
--    restrictive foreign-key deletion behavior unless the contract is deliberately
--    changed; do not introduce cascading deletion implicitly.
--
-- 3. Evidence kind: the row does not record whether the intent is for an Identity
--    Document or Biometric Capture. Add a bounded, non-null kind. Although Issue 007's
--    HTTP route accepts only `identity_document`, the approved shared schema must also
--    remain usable by Issue 008's `biometric_capture`; arbitrary strings must fail.
--
-- 4. Storage identity: there is no storage key. Each intent needs one non-null key,
--    and two intents must never point at the same key. This is separate from the UUID
--    primary key because MinIO addresses the stored object by storage key. DECISION:
--    the application constructs this key using its application-generated intent UUID
--    and supplies both values to the insert; the database still enforces uniqueness.
--
-- 5. Lifecycle: add expiry and status. Use timestamptz for an absolute expiry instant.
--    Status must be non-null, start as `pending`, and admit exactly `pending`,
--    `confirmed`, `superseded`, `expired`, and `validation_failed`.
--
-- 6. Recorded outcomes: the contract requires a bounded nullable failure code, a
--    confirmation timestamp, and `object_deleted_at`. Do not use free-form failure
--    text. The only failure reason currently frozen by Issue 007 is
--    `identity_number_mismatch`; stop and discuss before inventing more reasons.
--    Consider database checks that prevent incoherent combinations, for example a
--    failure code on a non-validation-failed row or a confirmed timestamp on a row
--    that is not confirmed.
--
-- 7. Time needed by later behavior: decide which timestamp records creation and which
--    timestamp lets cleanup determine when an intent entered `superseded`, `expired`,
--    or `validation_failed`. A creation timestamp alone does not precisely measure the
--    24-hour grace period after a later status change. The plan does not freeze the
--    column name, so make this decision explicit rather than guessing silently.
--
-- 8. Central concurrency invariant: the migration has no partial unique index. Add a
--    database-enforced rule that permits at most one `pending` row for the same
--    (Verification Session, kind), while allowing multiple historical non-pending
--    rows. A normal UNIQUE(session, kind) constraint would be too strict because it
--    would reject that history.
--
-- 9. DECISION (frozen): kind-specific size and media rules are owned by application
--    code and are not snapshotted in Upload Intent columns. Store only the bounded
--    kind needed to select that mapping. Confirmation uses the currently deployed
--    mapping, so do not add expected-size or expected-media-type columns.
--
-- 10. DECISION (revised and frozen): the application generates the Upload Intent UUID
--     before persistence; do not add a database UUID default that silently changes
--     ownership. The application uses that ID to construct the storage key, presigns
--     outside a transaction, then supplies both ID and key to the guarded
--     supersede/insert transaction. Signing failure writes nothing, and a stale
--     transactional re-read discards the unreturned URL.

-- REVIEW PASS 2 (blocking): the revision now has the main lifecycle columns and the
-- correct shape for a partial unique index, but it is not executable yet. Revise the
-- following items yourself before starting the proof.
--
-- A. Each named domain constraint is missing the SQL `CHECK` keyword. PostgreSQL
--    requires `CONSTRAINT name CHECK (predicate)`; a constraint name followed
--    directly by parentheses is invalid syntax.
--
-- B. The second kind is misspelled as `biometric capture`. The frozen database/API
--    value is exactly `biometric_capture`. This is not presentation text: the exact
--    underscore-delimited value must agree across SQL, Go, and HTTP contracts.
--
-- C. `storage_key` is non-null but not unique. The partial index protects only one
--    pending row per (Verification Session, kind); it does not stop two different
--    sessions or two historical intents from pointing to the same MinIO key. Enforce
--    storage-key uniqueness separately.
--
-- D. Explain the `uuid` type chosen for `storage_key`. It is valid only if the full
--    MinIO object key is deliberately a bare UUID. If the application will construct
--    a path/prefix around the intent UUID, a PostgreSQL uuid cannot store that key.
--    Make the type match the complete value sent to the object-storage adapter.
--
-- E. The source contract calls the nullable bounded value a failure code, while this
--    migration calls it `failure_reason`. Choose one term and make SQL, application,
--    HTTP details, and documentation agree; do not allow two names for one concept.
--
-- F. The current failure check bounds a non-null value but does not connect it to
--    status. As written, `validation_failed` may have NULL failure data, while a
--    `pending` or `confirmed` row may carry `identity_number_mismatch`. Add a database
--    invariant for the valid status/failure combinations.
--
-- G. `confirmed_at` has the same coherence problem: a confirmed row may have NULL,
--    and a pending row may have a confirmation timestamp. Decide and enforce the
--    valid status/timestamp combinations. Also protect the cleanup invariant so
--    `object_deleted_at` cannot describe deletion of a confirmed accepted object.
--
-- H. `latest_status_change_at` is a reasonable answer to the cleanup timing problem,
--    but a default covers only insertion. Document and later prove that every status
--    mutation updates it; otherwise cleanup will measure from intent creation rather
--    than from supersession, expiry, or validation failure.
--
-- I. Terminate the CREATE UNIQUE INDEX statement. The table statement is terminated,
--    but the final index statement currently has no semicolon.
CREATE TABLE upload_intents(
  id uuid PRIMARY KEY,
  verification_session_id uuid NOT NULL REFERENCES verification_sessions(id),
  kind text NOT NULL,
  storage_key text NOT NULL UNIQUE,
  status text NOT NULL default 'pending',
  created_at timestamptz NOT NULL default now(),
  latest_status_change_at timestamptz NOT NULL default now(),
  expires_at timestamptz NOT NULL,
  confirmed_at timestamptz,
  object_deleted_at timestamptz,
  failure_code text,
  CONSTRAINT upload_intent_kind_chk CHECK (
    kind IN ('identity_document', 'biometric_capture')
  ),
  CONSTRAINT upload_intent_status_chk CHECK (
    status IN ('pending', 'confirmed', 'superseded', 'expired', 'validation_failed')
  ),
  CONSTRAINT upload_intent_failure_code_chk CHECK (
    failure_code IN ('identity_number_mismatch')
  ),
  CONSTRAINT upload_intent_storage_key_not_empty_check
    CHECK (storage_key <> ''),
  CONSTRAINT upload_intent_status_failure_code_chk CHECK (
    (
      status = 'validation_failed'
      AND failure_code IS NOT NULL
      AND failure_code IN ('identity_number_mismatch')
    ) OR
    (
      status <> 'validation_failed'
      AND failure_code IS NULL
    )
  ),
  CONSTRAINT upload_intent_confirmed_object_deleted_at CHECK (
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
  CONSTRAINT upload_intent_restrict_deleted CHECK (
      object_deleted_at IS NULL
      OR status IN ('superseded', 'expired', 'validation_failed')
  )
);

CREATE UNIQUE INDEX one_pending_one_kind_idx
ON upload_intents (verification_session_id, kind)
WHERE status = 'pending';
