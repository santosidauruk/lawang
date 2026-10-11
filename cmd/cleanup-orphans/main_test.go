package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunInvalidConfigurationUsesStderrAndNonZeroExit(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(context.Background(), &stdout, &stderr)

	if exitCode != 1 {
		t.Errorf("run() exit code = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid configuration") {
		t.Errorf("stderr = %q, want safe invalid-configuration summary", stderr.String())
	}
}

func TestRunHonorsCanceledParentWithoutPrintingConfigurationOrRawError(t *testing.T) {
	const databaseSecret = "cleanup-database-secret"
	const storageSecret = "cleanup-storage-secret"
	setCleanupCommandEnvironment(t, databaseSecret, storageSecret)
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	startedAt := time.Now()

	exitCode := run(parentCtx, &stdout, &stderr)

	if exitCode != 1 {
		t.Errorf("run() exit code = %d, want 1", exitCode)
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Errorf("run() elapsed = %s, want canceled parent to bound startup", elapsed)
	}
	combined := stdout.String() + stderr.String()
	for _, sensitive := range []string{databaseSecret, storageSecret, "postgresql://", "context canceled"} {
		if strings.Contains(combined, sensitive) {
			t.Errorf("command output exposes %q: %s", sensitive, combined)
		}
	}
	if !strings.Contains(stdout.String(), "database unavailable") {
		t.Errorf("stdout = %q, want safe database-unavailable summary", stdout.String())
	}
}

func setCleanupCommandEnvironment(t *testing.T, databaseSecret, storageSecret string) {
	t.Helper()
	environment := map[string]string{
		"DATABASE_URL":            "postgresql://lawang:" + databaseSecret + "@127.0.0.1:1/lawang",
		"S3_INTERNAL_ENDPOINT":    "http://127.0.0.1:9000",
		"S3_PUBLIC_ENDPOINT":      "http://127.0.0.1:9000",
		"S3_REGION":               "us-east-1",
		"S3_ACCESS_KEY":           "cleanup-access-key",
		"S3_SECRET_KEY":           storageSecret,
		"S3_BUCKET":               "cleanup-test",
		"S3_USE_PATH_STYLE":       "true",
		"PROVIDER_WEBHOOK_SECRET": "cleanup-provider-secret",
		"CLEANUP_BATCH_SIZE":      "10",
	}
	for key, value := range environment {
		t.Setenv(key, value)
	}
}
