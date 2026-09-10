package deterministicextractor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/santosidauruk/lawang/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang/internal/application/artifact"
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

func TestExtractorReturnsConfiguredErrorBeforeConfiguredResult(t *testing.T) {
	storageKey := "verification-sessions/session-1/identity_document/intent-2"
	wantErr := errors.New("configured extraction failure")
	extractor := deterministicextractor.New(
		map[string]artifact.DocumentExtraction{
			storageKey: {IdentityNumber: "must-not-be-returned"},
		},
		map[string]error{storageKey: wantErr},
	)

	got, err := extractor.Extract(context.Background(), storageKey)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Extract() error = %v, want configured error", err)
	}
	if got != (artifact.DocumentExtraction{}) {
		t.Errorf("Extract() = %#v, want zero result", got)
	}
}

func TestExtractorReturnsExplicitErrorForUnknownStorageKey(t *testing.T) {
	extractor := deterministicextractor.New(nil, nil)

	got, err := extractor.Extract(
		context.Background(),
		"verification-sessions/session-1/identity_document/unknown-intent",
	)
	if !errors.Is(err, deterministicextractor.ErrMissingExtraction) {
		t.Fatalf("Extract() error = %v, want ErrMissingExtraction", err)
	}
	if got != (artifact.DocumentExtraction{}) {
		t.Errorf("Extract() = %#v, want zero result", got)
	}
}

func TestExtractorHonorsCanceledContextBeforeConfiguredBehavior(t *testing.T) {
	storageKey := "verification-sessions/session-1/identity_document/intent-3"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	extractor := deterministicextractor.New(
		map[string]artifact.DocumentExtraction{
			storageKey: {IdentityNumber: "must-not-be-returned"},
		},
		map[string]error{storageKey: errors.New("must-not-be-returned")},
	)

	got, err := extractor.Extract(ctx, storageKey)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract() error = %v, want context.Canceled", err)
	}
	if got != (artifact.DocumentExtraction{}) {
		t.Errorf("Extract() = %#v, want zero result", got)
	}
}
