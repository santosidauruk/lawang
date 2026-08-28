package verdict

import (
	"errors"
	"fmt"
)

type Verdict string

const (
	Verified Verdict = "verified"
	Rejected Verdict = "rejected"
)

func (p Verdict) String() string { return string(p) }

type RejectionReason string

const (
	DocumentInvalid     RejectionReason = "document_invalid"
	BiometricMismatch   RejectionReason = "biometric_mismatch"
	IdentityNotVerified RejectionReason = "identity_not_verified"
	SuspectedFraud      RejectionReason = "suspected_fraud"
)

func (r RejectionReason) String() string { return string(r) }

type ProviderVerdict struct {
	verdict Verdict
	reason  RejectionReason
}

func (p ProviderVerdict) Verdict() Verdict {
	return p.verdict
}

func (p ProviderVerdict) RejectionReason() (RejectionReason, bool) {
	if p.verdict != Rejected {
		return "", false
	}

	return p.reason, true
}

func ParseProviderVerdict(rawVerdict string, rawReason *string) (ProviderVerdict, error) {
	switch Verdict(rawVerdict) {
	case Verified:
		if rawReason != nil {
			return ProviderVerdict{}, errors.New("reason must be absent on verified verdict")
		}
		return ProviderVerdict{verdict: Verified}, nil
	case Rejected:
		if rawReason == nil {
			return ProviderVerdict{}, errors.New("reason is required on rejected verdict")
		}
		reason, err := parseRejectionReason(*rawReason)
		if err != nil {
			return ProviderVerdict{}, err
		}
		return ProviderVerdict{verdict: Rejected, reason: reason}, nil
	default:
		return ProviderVerdict{}, fmt.Errorf("unknown verdict: %q", rawVerdict)
	}
}

func parseRejectionReason(raw string) (RejectionReason, error) {
	reason := RejectionReason(raw)
	switch reason {
	case DocumentInvalid, BiometricMismatch, IdentityNotVerified, SuspectedFraud:
		return reason, nil
	default:
		return "", fmt.Errorf("unknown rejection reason: %q", reason)
	}
}
