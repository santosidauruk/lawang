# Resume-token authentication

`POST /verification-sessions` creates the UUID and default `created` state in
PostgreSQL. The application generates 32 cryptographically random bytes, encodes
them as an opaque base64url resume token, hashes that token with SHA-256, and sends
only the 32-byte hash to PostgreSQL. The raw token is returned once in the create
response; later reads never return it.

The hash is deterministic because resume authentication must hash the supplied
credential the same way. The in-process comparison is constant-time and checks
equal lengths before comparing. PostgreSQL also enforces a unique, exactly 32-byte
hash and provides a typed `(id, resume_token_hash)` lookup for relational proof.

Bearer parsing is deliberately small and explicit. `Bearer` is case-insensitive,
but the header must contain exactly one non-whitespace credential. Missing and
malformed headers have distinct stable `401` error codes. Compatibility preserves
the legacy distinction between an unknown session (`404`) and a wrong token (`401`).

Expiry is a real-world fact, so the use case receives a clock. Creation sets
`expires_at` to the clock plus 30 minutes, and resume rejects `now >= expires_at`
with `410 SESSION_EXPIRED`. Fixed-clock tests prove the boundary without sleeps.

Implementation evidence:

- `go test ./internal/application/session ./internal/adapter/httpapi`
- `go test -v ./tests/integration -run 'TestVerificationSessionMigrationAndSQLProof|TestApplicantCreatesAndResumesSessionOverHTTPWithPostgreSQL' -count=1`
- `sql/proofs/002_resume_token_authentication.sql` proves authorized lookup,
  wrong-hash refusal, hash uniqueness, 32-byte storage, and absence of a raw-token
  column in a disposable transaction.
