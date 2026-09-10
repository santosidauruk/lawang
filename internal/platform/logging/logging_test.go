package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/santosidauruk/lawang/internal/platform/logging"
)

func TestNewWritesLevelledJSON(t *testing.T) {
	var output bytes.Buffer
	logger := logging.New(&output, slog.LevelInfo)

	logger.Debug("hidden")
	logger.Info("api started", "address", ":8080")

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not JSON: %v", err)
	}
	if got, want := entry["level"], "INFO"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
	if got, want := entry["msg"], "api started"; got != want {
		t.Errorf("msg = %v, want %v", got, want)
	}
	if got, want := entry["address"], ":8080"; got != want {
		t.Errorf("address = %v, want %v", got, want)
	}
}
