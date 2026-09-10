# Lawang Issues

This directory is the local Markdown issue tracker for the approved Go rebuild plan.
Issues are ordered contracts and must be implemented in dependency order.

## Triage Vocabulary

- `needs-triage`: maintainer must evaluate the issue.
- `needs-info`: waiting for required information.
- `ready-for-agent`: fully specified and safe for an AFK agent.
- `ready-for-human`: requires human authorship, review, or a decision.
- `wontfix`: intentionally not actioned.

Every new issue starts with `needs-triage`. `Type: AFK` means an agent may complete
the issue without routine human interaction after blockers are resolved. `Type: HITL`
means the issue contains a required human decision or learning checkpoint.

## Aturan penjelasan istilah teknis Inggris

Saat agent pertama kali memperkenalkan istilah teknis Inggris yang sulit
diterjemahkan secara ringkas, seperti `coordinator`, `seam`, atau `predicate`, agent
harus langsung menambahkan satu kalimat penjelas dalam tanda kurung. Penjelasan harus
menggunakan bahasa Indonesia yang casual dan menerangkan fungsi istilah tersebut
dalam konteks pekerjaan yang sedang dibahas, bukan hanya terjemahan kamus.

## User Outcomes

- **US-01:** As an operator, I can bootstrap and inspect an isolated Go/PostgreSQL
  service without affecting the TypeScript project.
- **US-02:** As an Applicant, I can create a Verification Session and securely resume
  it with a one-time-issued token.
- **US-03:** As an operator, I can trust that session changes are state-guarded,
  atomic, and auditable.
- **US-04:** As an Applicant, I can submit immutable Personal Details with safe replay
  and conflict behavior.
- **US-05:** As an Applicant, I can upload and confirm an Identity Document, receiving
  deterministic local-validation results.
- **US-06:** As an Applicant, I can upload and confirm a Biometric Capture and become
  eligible for provider submission only when both required artifacts are accepted.
- **US-07:** As an Applicant, I can submit verification asynchronously and receive an
  idempotently applied provider verdict.
- **US-08:** As an operator, I can expire stalled sessions, clean only safe orphaned
  objects, inspect redacted history, and run the documented service reliably.

## Breakdown

| # | Issue | Type | Blocked by | Outcomes |
| --- | --- | --- | --- | --- |
| 001 | [Scaffold an isolated runnable Go SQL lab](./001-scaffold-isolated-runnable-go-sql-lab.md) | HITL | Module-path decision | US-01 |
| 002 | [Create and resume a Verification Session](./002-create-and-resume-verification-session.md) | AFK | 001 | US-02 |
| 003 | [Append Session Events atomically](./003-append-session-events-atomically.md) | AFK | 002 | US-03 |
| 004 | [Enforce the Verification Session state machine](./004-enforce-verification-session-state-machine.md) | AFK | 003 | US-03 |
| 005 | [Harden the session HTTP-to-SQL path](./005-harden-session-http-to-sql-path.md) | AFK | 004 | US-02, US-03 |
| 006 | [Submit immutable Personal Details](./006-submit-immutable-personal-details.md) | AFK | 005, old-contract evidence | US-04 |
| 007 | [Confirm Identity Document uploads](./007-confirm-identity-document-uploads.md) | HITL | 006, parity gate | US-05 |
| 008 | [Confirm Biometric Capture uploads](./008-confirm-biometric-capture-uploads.md) | HITL | 007 | US-06 |
| 009 | [Submit verification and apply signed verdicts](./009-submit-verification-and-apply-signed-verdicts.md) | HITL | 008 | US-07 |
| 010 | [Operate expiry, cleanup, audit, and the complete MVP](./010-operate-expiry-cleanup-audit-and-mvp.md) | HITL | 009 | US-08 |

## Critical Path

```text
module path -> 001 -> 002 -> 003 -> 004 -> 005 -> 006
                                               |
                                  compatibility report
                                               v
                     007 -> 008 -> 009 -> 010
```

## Breakdown Assessment

The approved plan explicitly fixes ten sequential issues. Issues 001, 002, 006, 007,
008, 009, and 010 deliver observable end-to-end behavior. Issues 003-005 are thinner
foundational proofs/refactors and are not ideal product-level vertical slices; they
remain separate because the approved learning sequence treats transaction proofs,
the pure state machine, and final adapter boundaries as independently reviewed
learning outcomes.

Do not merge Issue 003-005 during implementation without revising `docs/plan-go.md`.
If product throughput becomes more important than the learning sequence, revisit
those boundaries first.
