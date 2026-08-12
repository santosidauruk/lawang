# Confirm Biometric Capture Uploads

Status: ready-for-human  
Type: HITL  
Labels: ready-for-human  
Source: `docs/plan-go.md` sections 4-5, 8-9, 14, 16 Issue 8, and 17.2

## User stories covered

- US-06: Upload and confirm a Biometric Capture and become submission-ready only
  after both required Verification Artifacts are accepted.

## What to build

Extend the proven upload-confirm path to Biometric Capture with its own metadata
rules and no document extraction. Successful confirmation creates an accepted
Biometric Capture Verification Artifact and advances the ordered session state.
Submission readiness is derived from accepted artifact records, never object presence
or intent status alone.

## Required learning checkpoint

The user implements the first kind-specific domain rule or object-storage adapter
operation. The agent reviews whether Issue 007's abstractions should be reused or
remain explicit. Generalize only when the shared invariant is real; two similar
handlers alone are insufficient evidence.

## Active collaboration plan

The approved user/agent checkpoint split is documented in
[`docs/plans/issue_008/README.md`](../plans/issue_008/README.md). Checkpoint 1 is the
active learning checkpoint. Do not begin later checkpoints before the active
checkpoint reaches its documented review gate.

## Scope boundaries

- No document extractor for Biometric Capture.
- Do not implement provider submission yet.
- Preserve the public ordering: identity document before biometric capture.
- Do not build a generic artifact framework that erases kind-specific constraints.

## Domain and storage invariants

- Biometric Capture allows JPEG/PNG, non-zero, <= 5 MiB.
- Identity and biometric storage keys cannot collide.
- A Biometric Upload Intent creates at most one accepted Biometric Verification
  Artifact.
- Successful confirm advances from `identity_document_uploaded` to
  `biometric_capture_uploaded` and appends one `confirm_biometric_capture` event
  atomically.
- Submission readiness requires both accepted Verification Artifact rows.

## Acceptance criteria

- [ ] The checkpoint implementation receives critical review before generalization.
- [ ] Upload URL creation accepts `biometric_capture` and preserves all intent TTL,
      supersession, key uniqueness, auth, and replay rules from Issue 007.
- [ ] Confirm uses real `HeadObject` metadata and enforces JPEG/PNG, non-zero, and
      5 MiB maximum without invoking the document extractor.
- [ ] Successful confirm creates one artifact, advances state, and appends one event
      of type `confirm_biometric_capture` atomically; replay/concurrency creates no
      duplicates.
- [ ] Wrong kind, PDF biometric, oversized/empty object, wrong state, superseded or
      expired intent, and stale transactional re-read return bounded errors.
- [ ] A guarded readiness query/use case returns ready only when both accepted
      artifact records exist for the same session.
- [ ] Object presence, confirmed intent alone, or a validation-failed identity intent
      cannot satisfy readiness.

## API examples

```json
{"kind":"biometric_capture"}
```

The upload URL and confirm routes retain Issue 007's method, auth, and response
shapes. Successful confirm returns the current session summary with
`biometric_capture_uploaded`.

## SQL proof

Prove kind-bounded uniqueness, non-colliding keys, one accepted artifact per
`(session, kind)`, and a readiness query that requires both accepted artifact kinds.
Include negative fixtures for stored object/intent without accepted artifact.

## Tests

- User-authored kind-specific rule/adapter test, then sibling unit/application tests.
- HTTP contract tests for biometric kind and content rules.
- PostgreSQL replay/concurrency/readiness tests.
- Real MinIO JPEG/PNG success plus PDF/size/empty failure tests.
- Regression tests for Identity Document behavior.

## Verification commands

Run the full quality gate and explicit PostgreSQL/MinIO integration suites. Include a
race test demonstrating idempotent concurrent biometric confirm.

## Implementation evidence and learning note

Retain checkpoint review, metadata constraint results, readiness SQL output, race
tests, and identity regression output. Add
`docs/learning/008-kind-specific-biometric-upload.md`.

## Blocked by

- [007 - Confirm Identity Document uploads](./007-confirm-identity-document-uploads.md)
- Required user-authored checkpoint described above.
