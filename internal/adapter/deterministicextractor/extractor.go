// Package deterministicextractor provides the deterministic DocumentExtractor
// used by the Issue 007 integration path. It is intentionally not an OCR adapter.
package deterministicextractor

import (
	"context"
	"errors"

	"github.com/santosidauruk/lawang/internal/application/artifact"
)

// ErrMissingExtraction is returned when no configured result or error exists for
// a storage key. The application service converts boundary errors into its safe,
// bounded error contract.
var ErrMissingExtraction = errors.New("missing deterministic document extraction")

// Extractor is configured explicitly by storage key. Results and Errors are kept
// separate so tests can deterministically exercise both successful extraction and
// extractor failure without deriving an identity number from object bytes, names,
// or metadata.
type Extractor struct {
	results map[string]artifact.DocumentExtraction
	errors  map[string]error
}

// New constructs a deterministic extractor from explicit per-storage-key maps.
// The first behavior to implement is Extract: check Errors, then Results, then
// return ErrMissingExtraction for an unknown key.
func New(
	results map[string]artifact.DocumentExtraction,
	errors map[string]error,
) *Extractor {
	return &Extractor{
		results: results,
		errors:  errors,
	}
}

func (e *Extractor) Extract(ctx context.Context, storageKey string) (artifact.DocumentExtraction, error) {
	if err := ctx.Err(); err != nil {
		return artifact.DocumentExtraction{}, err
	}

	if err, exists := e.errors[storageKey]; exists {
		return artifact.DocumentExtraction{}, err
	}

	result, exists := e.results[storageKey]
	if !exists {
		return artifact.DocumentExtraction{}, ErrMissingExtraction
	}

	return result, nil

}
