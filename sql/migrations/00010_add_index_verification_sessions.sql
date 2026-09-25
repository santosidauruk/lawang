-- +goose Up

create index idx_applicant_stage_session
on verification_sessions (expires_at, id)
where status in (
  'created',
  'personal_details_submitted',
  'identity_document_uploaded',
  'biometric_capture_uploaded'
);

create index idx_verification_pending_session
on verification_sessions (verification_deadline_at, id)
where status = 'verification_pending' and verification_deadline_at is not null;
