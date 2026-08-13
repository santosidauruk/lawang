# Kind-Specific Biometric Upload

This is the working learning record for Issue 008. Conclusions are recorded only
after the corresponding RED-GREEN checkpoint has been exercised and reviewed.

## Checkpoint 1: kind policy and Biometric Upload Intent

Checkpoint 1 is complete. The retained user tracer proves that
`UploadIntentService.Create` accepts exact kind `biometric_capture` only when the
Verification Session is `identity_document_uploaded`. It generates a fresh Upload
Intent UUID, derives
`verification-sessions/{sessionID}/biometric_capture/{intentID}`, presigns for five
minutes before opening a database transaction, then re-reads the session and
atomically supersedes/inserts the kind-scoped pending intent.

The initial state differs by evidence kind because the public workflow is ordered:
Identity Document upload authorization follows Personal Details, while Biometric
Capture upload authorization follows an accepted Identity Document. This state rule
is kind-specific. UUID generation, five-minute TTL, external-I/O ordering, guarded
transaction, and supersession by `(session, kind)` are shared invariants.

The implementation keeps the two allowed kinds and their required states explicit.
It does not introduce a registry or generic artifact framework. Exact kind is safe as
the storage-key segment only after invalid kinds have been rejected before session,
presigner, or transaction work.

Application sibling tests prove wrong initial state, stale transactional re-read,
presign failure without writes, preservation of confirmed Identity Document intent
history, repeated-create freshness, identity/biometric key isolation, and invalid-kind
short-circuiting. The memory transaction proves application orchestration only; real
one-pending-per-kind behavior under race remains a PostgreSQL Checkpoint 3 proof.
These sibling tests were GREEN on their first run because the reviewed user minimal
GREEN already covered the shared flow; no artificial RED or additional production
change was introduced.

Verification evidence on 2026-08-13:

```text
GOCACHE=/tmp/lawang-go-build go test ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -run 'UploadIntent' -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./internal/application/artifact
PASS

GOCACHE=/tmp/lawang-go-build go test -race ./internal/application/artifact -count=1
ok github.com/santosidauruk/lawang-go/internal/application/artifact

GOCACHE=/tmp/lawang-go-build go vet ./...
PASS

GOCACHE=/tmp/lawang-go-build STATICCHECK_CACHE=/tmp/lawang-go-staticcheck make staticcheck
PASS
```
