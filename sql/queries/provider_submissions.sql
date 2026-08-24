-- ISSUE 009 CHECKPOINT 1 — USER-OWNED QUERY SCAFFOLD
--
-- Add the guarded Verification Session lock/read and exact pending transition named
-- queries here. Run sqlc generate; never edit generated Go manually.

-- name: SetPendingVerification :execrows
update verification_sessions
set 
  status = 'verification_pending',
  verification_deadline_at = sqlc.arg(submitted_at)::timestamptz + interval '24 hours',
  updated_at = sqlc.arg(submitted_at)::timestamptz
where status = 'biometric_capture_uploaded' and id = sqlc.arg(session_id);