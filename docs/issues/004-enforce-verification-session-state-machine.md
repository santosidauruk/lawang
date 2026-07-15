# Enforce the Verification Session State Machine

Status: needs-triage  
Type: AFK  
Labels: needs-triage  
Source: `docs/plan-go.md` sections 4.1-4.2, 6, 7.3, 8, 11, 14, and 16 Issue 4

## User stories covered

- US-03: Trust that session changes are state-guarded, terminal-safe, and auditable.

## What to build

Implement the Verification Session state machine as infrastructure-free domain code
and enforce expected-state guards in PostgreSQL. Demonstrate that one application
transition uses both layers and commits its Session Event atomically while stale,
forbidden, and terminal attempts leave no partial writes.

## Scope boundaries

- Define all planned states and transitions, but do not expose future upload/provider
  routes early.
- Do not put database or HTTP types in the domain.
- Scheduled and lazy expiry orchestration remains Issue 010; this issue defines the
  pure expiry rule and applicable-deadline semantics.

## Domain and data invariants

- Exact public state strings come from `CONTEXT.md`.
- Required forward order is Personal Details, Identity Document, Biometric Capture,
  pending provider, then verified/rejected.
- `expired` is allowed only from an overdue non-terminal state.
- `verified`, `rejected`, and `expired` never transition.
- A DB update includes the expected prior state so concurrent/stale transitions lose
  safely rather than overwriting newer state.

## Acceptance criteria

- [ ] Domain types model every public state without stringly typed transition logic.
- [ ] Table-driven tests cover every allowed transition, representative forbidden
      transitions, all terminal states, and expiry before/after the applicable
      deadline.
- [ ] Domain tests run with no DB, filesystem, network, or real clock.
- [ ] A named guarded-update query changes a row only from its expected state and
      surfaces stale/concurrent failure distinctly.
- [ ] A PostgreSQL concurrency proof shows two competing transitions cannot both win.
- [ ] The application transition commits exactly one safe Session Event on success
      and none on stale, forbidden, or terminal failure.
- [ ] Existing Issue 002 HTTP behavior remains unchanged.

## API examples

No future route is exposed. Existing session summaries may now use the domain state
type internally but their public JSON remains byte-for-byte compatible.

## SQL proof

Use two connections or an equivalent deterministic integration harness to attempt
competing expected-state updates. Prove only one update affects a row and only the
winner appends a Session Event.

## Tests

- Exhaustive table-driven domain transition tests.
- Fixed-clock expiry boundary tests.
- Application tests for success, forbidden, terminal, and stale outcomes.
- PostgreSQL concurrency and rollback integration tests.
- Existing create/resume contract regression suite.

## Verification commands

Run domain tests independently, then the repository quality gate and disposable
PostgreSQL concurrency proof. Do not use `time.Sleep` as the synchronization proof.

## Implementation evidence and learning note

Retain the transition matrix, domain test output, concurrency proof, and unchanged API
snapshot. Add `docs/learning/004-session-state-machine.md` explaining pure domain
rules, expected-state updates, terminal safety, and clock-controlled expiry.

## Blocked by

- [003 - Append Session Events atomically](./003-append-session-events-atomically.md)

