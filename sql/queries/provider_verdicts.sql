-- ISSUE 009 CHECKPOINT 5 — USER-OWNED QUERY SCAFFOLD
--
-- Add the handwritten sqlc queries for locking a Verification Session and applying
-- guarded verified/rejected terminal outcomes. Do not edit generated sqlc files.

-- 6. `[sql query][postgres adapter]` User menulis guarded verified update dari
--    `verification_pending` ke `verified` beserta `verified_at`.

-- name: UpdateSessionToVerified :execrows
update verification_sessions
set
  status = 'verified',
  verified_at = sqlc.arg(verified_at),
  updated_at = sqlc.arg(verified_at)
where id = sqlc.arg(session_id) and status = 'verification_pending';


-- 7. `[sql query][postgres adapter]` User menulis guarded rejected update dari
--    `verification_pending` ke `rejected` beserta `rejected_at` dan exact reason.
-- name: UpdateSessionToRejected :execrows
update verification_sessions
set
  status = 'rejected',
  rejected_at = sqlc.arg(rejected_at),
  rejection_reason = sqlc.arg(rejection_reason),
  updated_at = sqlc.arg(rejected_at)
where id = sqlc.arg(session_id) and status = 'verification_pending';
