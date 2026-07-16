package sessionevent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

type Type string

const (
	SubmitPersonalDetails   Type = "submit_personal_details"
	ConfirmIdentityDocument Type = "confirm_identity_document"
	ConfirmBiometricCapture Type = "confirm_biometric_capture"
	SubmitSession           Type = "submit_session"
	VerificationPassed      Type = "verification_passed"
	VerificationFailed      Type = "verification_failed"
	Expire                  Type = "expire"
)

func ParseType(value string) (Type, error) {
	eventType := Type(value)
	switch eventType {
	case SubmitPersonalDetails,
		ConfirmIdentityDocument,
		ConfirmBiometricCapture,
		SubmitSession,
		VerificationPassed,
		VerificationFailed,
		Expire:
		return eventType, nil
	default:
		return "", fmt.Errorf("unknown Session Event type %q", value)
	}
}

func (t Type) String() string { return string(t) }

type Outcome string

const (
	OutcomeAccepted              Outcome = "accepted"
	OutcomeLocalValidationFailed Outcome = "local_validation_failed"
)

type Metadata struct {
	outcome Outcome
}

type Event struct {
	ID         uuid.UUID
	SessionID  uuid.UUID
	Type       Type
	Metadata   Metadata
	OccurredAt time.Time
}

func EmptyMetadata() Metadata { return Metadata{} }

func NewMetadata(outcome Outcome) (Metadata, error) {
	switch outcome {
	case OutcomeAccepted, OutcomeLocalValidationFailed:
		return Metadata{outcome: outcome}, nil
	default:
		return Metadata{}, fmt.Errorf("unknown Session Event outcome %q", outcome)
	}
}

func (m Metadata) Outcome() Outcome { return m.outcome }

func (m Metadata) MarshalJSON() ([]byte, error) {
	if m.outcome == "" {
		return []byte("{}"), nil
	}
	if _, err := NewMetadata(m.outcome); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Outcome Outcome `json:"outcome"`
	}{Outcome: m.outcome})
}

func ParseMetadata(data []byte) (Metadata, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return Metadata{}, fmt.Errorf("decode Session Event metadata: %w", err)
	}
	if object == nil {
		return Metadata{}, fmt.Errorf("session event metadata must be an object")
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return Metadata{}, err
	}
	for key := range object {
		if key != "outcome" {
			return Metadata{}, fmt.Errorf("unknown Session Event metadata field %q", key)
		}
	}
	rawOutcome, exists := object["outcome"]
	if !exists {
		return EmptyMetadata(), nil
	}
	var outcome Outcome
	if err := json.Unmarshal(rawOutcome, &outcome); err != nil {
		return Metadata{}, fmt.Errorf("decode Session Event outcome: %w", err)
	}
	return NewMetadata(outcome)
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("session event metadata must contain one JSON value")
		}
		return fmt.Errorf("decode Session Event metadata: %w", err)
	}
	return nil
}
