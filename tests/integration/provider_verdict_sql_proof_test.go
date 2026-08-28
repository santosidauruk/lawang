package integration_test

import (
	"strings"
	"testing"
)

func TestSignedProviderVerdictSQLProof(t *testing.T) {
	ctx, container, _ := openUploadIntentDatabase(t)

	for _, migration := range []struct {
		hostPath      string
		containerPath string
	}{
		{"../../sql/migrations/00006_create_verification_artifacts.sql", "/tmp/00006.sql"},
		{"../../sql/migrations/00007_add_provider_submission_outbox.sql", "/tmp/00007.sql"},
		{"../../sql/migrations/00008_create_webhook_events.sql", "/tmp/00008.sql"},
	} {
		runPSQLFile(t, ctx, container, migration.hostPath, migration.containerPath)
	}

	output := runPSQLFile(
		t,
		ctx,
		container,
		"../../sql/proofs/009_signed_provider_verdicts.sql",
		"/tmp/009_signed_provider_verdicts.sql",
	)
	if want := "proof passed: one verified and one rejected signed verdict outcome committed atomically"; !strings.Contains(output, want) {
		t.Fatalf("SQL proof output missing %q; output:\n%s", want, output)
	}
}
