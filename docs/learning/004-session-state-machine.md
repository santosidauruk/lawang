# Verification Session State Machine

## Pure domain rules

The state machine lives in `internal/domain/verificationsession` and depends only on
the standard library plus the bounded `sessionevent.Type`. Callers supply time and
the applicable deadline, so domain tests never use a database, network, filesystem,
or real clock.

The canonical forward transition matrix is:

| Expected state | Session Event action | Resulting state |
| --- | --- | --- |
| `created` | `submit_personal_details` | `personal_details_submitted` |
| `personal_details_submitted` | `confirm_identity_document` | `identity_document_uploaded` |
| `identity_document_uploaded` | `confirm_biometric_capture` | `biometric_capture_uploaded` |
| `biometric_capture_uploaded` | `submit_session` | `verification_pending` |
| `verification_pending` | `verification_passed` | `verified` |
| `verification_pending` | `verification_failed` | `rejected` |

Every other forward state/action pair is forbidden. `verified`, `rejected`, and
`expired` return the distinct terminal-state error for every later attempt.

Expiry is a separate pure rule because it needs the applicable deadline. Every
non-terminal state may become `expired` when `now >= deadline`; an attempt one
nanosecond before the deadline is rejected. The application records the action verb
`expire`, not the resulting state string `expired`.

## Expected-state persistence guard

`GuardVerificationSessionState` performs one conditional update:

```sql
UPDATE verification_sessions
SET status = $1, updated_at = $2
WHERE id = $3 AND status = $4;
```

The PostgreSQL adapter requires exactly one affected row. Zero rows become
`ErrSessionTransitionStale`, which is distinct from a domain-forbidden or terminal
transition. The guard prevents an older caller from overwriting a state committed by
another transaction.

## Atomic application transition

The application first asks the pure domain state machine for the next state. Only an
allowed result enters `WithinTransaction`. Inside that transaction it applies the
expected-state update and appends exactly one action-based Session Event. Any stale
guard, append failure, forbidden action, terminal attempt, or premature expiry leaves
both state and event history unchanged.

## Implementation evidence

Focused domain and application tests:

```text
ok github.com/santosidauruk/lawang-go/internal/domain/verificationsession
ok github.com/santosidauruk/lawang-go/internal/application/session
```

The disposable PostgreSQL proof starts two callers on separate connections behind a
channel barrier, without `time.Sleep`. Both expect `created`; the result is exactly
one successful transition, one `ErrSessionTransitionStale`, and one persisted
`submit_personal_details` event:

```text
=== RUN   TestPostgresExpectedStateGuardAllowsOnlyOneCompetingTransition
--- PASS: TestPostgresExpectedStateGuardAllowsOnlyOneCompetingTransition
PASS
```

The existing HTTP adapter regression suite remains unchanged and passes, preserving
the Issue 002 create/resume JSON contract while the application now uses the domain
state type internally.
