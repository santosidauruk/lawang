package integration_test

import (
	"strings"
	"testing"
)

func TestAtomicProviderSubmissionOutboxSQLProof(t *testing.T) {
	ctx, container, _ := openUploadIntentDatabase(t)

	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	output := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/008_atomic_provider_submission_outbox.sql",
		"/tmp/008_atomic_provider_submission_outbox.sql",
	)
	for _, want := range []string{
		"proof passed: pending state, exact deadline, submit event, and unpublished outbox committed atomically",
		"proof passed: forced failure rolled back pending state, deadline, submit event, and outbox",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("SQL proof output missing %q; output:\n%s", want, output)
		}
	}
}
