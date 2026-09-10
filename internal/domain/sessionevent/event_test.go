package sessionevent_test

import (
	"encoding/json"
	"testing"

	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
)

func TestParseTypeAdmitsOnlySessionEventActionVerbs(t *testing.T) {
	allowed := []sessionevent.Type{
		sessionevent.SubmitPersonalDetails,
		sessionevent.ConfirmIdentityDocument,
		sessionevent.ConfirmBiometricCapture,
		sessionevent.SubmitSession,
		sessionevent.VerificationPassed,
		sessionevent.VerificationFailed,
		sessionevent.Expire,
	}

	for _, want := range allowed {
		got, err := sessionevent.ParseType(want.String())
		if err != nil {
			t.Fatalf("ParseType(%q) error = %v", want, err)
		}
		if got != want {
			t.Errorf("ParseType(%q) = %q, want %q", want, got, want)
		}
	}

	for _, value := range []string{"personal_details_submitted", "verified", "expired", "unknown"} {
		if _, err := sessionevent.ParseType(value); err == nil {
			t.Errorf("ParseType(%q) succeeded, want rejection", value)
		}
	}
}

func TestMetadataJSONSurfaceContainsOnlyTheBoundedOutcome(t *testing.T) {
	metadata, err := sessionevent.NewMetadata(sessionevent.OutcomeLocalValidationFailed)
	if err != nil {
		t.Fatalf("NewMetadata() error = %v", err)
	}

	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if string(encoded) != `{"outcome":"local_validation_failed"}` {
		t.Errorf("json.Marshal() = %s, want bounded outcome object", encoded)
	}

	decoded, err := sessionevent.ParseMetadata(encoded)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if decoded.Outcome() != sessionevent.OutcomeLocalValidationFailed {
		t.Errorf("decoded outcome = %q, want %q", decoded.Outcome(), sessionevent.OutcomeLocalValidationFailed)
	}

	for _, unsafe := range []string{
		`{"resume_token":"secret"}`,
		`{"outcome":"accepted","identity_number":"123"}`,
	} {
		if _, err := sessionevent.ParseMetadata([]byte(unsafe)); err == nil {
			t.Errorf("ParseMetadata(%s) succeeded, want rejection", unsafe)
		}
	}
}

func TestMetadataAdmitsOnlyBoundedNonSensitiveOutcome(t *testing.T) {
	for _, outcome := range []sessionevent.Outcome{
		sessionevent.OutcomeAccepted,
		sessionevent.OutcomeLocalValidationFailed,
	} {
		metadata, err := sessionevent.NewMetadata(outcome)
		if err != nil {
			t.Fatalf("NewMetadata(%q) error = %v", outcome, err)
		}
		if metadata.Outcome() != outcome {
			t.Errorf("metadata outcome = %q, want %q", metadata.Outcome(), outcome)
		}
	}

	if _, err := sessionevent.NewMetadata("raw provider response with identity number"); err == nil {
		t.Fatal("NewMetadata accepted an unbounded sensitive value")
	}
}
