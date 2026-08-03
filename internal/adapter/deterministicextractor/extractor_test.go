package deterministicextractor_test

import (
	"context"
	"testing"

	"github.com/santosidauruk/lawang-go/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
)

func TestExtractorReturnsConfiguredResult(t *testing.T) {
	storageKey := "verification-sessions/session-1/identity_document/intent-1"
	want := artifact.DocumentExtraction{IdentityNumber: "3174010101010001"}
	extractor := deterministicextractor.New(
		map[string]artifact.DocumentExtraction{storageKey: want},
		nil,
	)

	got, err := extractor.Extract(context.Background(), storageKey)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if got != want {
		t.Errorf("Extract() = %#v, want %#v", got, want)
	}
}
