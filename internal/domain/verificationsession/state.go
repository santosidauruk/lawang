package verificationsession

import (
	"errors"
	"fmt"
	"time"

	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

var (
	ErrTerminalState       = errors.New("terminal verification session state")
	ErrDeadlineNotReached  = errors.New("verification session deadline not reached")
	ErrTransitionForbidden = errors.New("verification session transition forbidden")
)

type State string

const (
	Created                  State = "created"
	PersonalDetailsSubmitted State = "personal_details_submitted"
	IdentityDocumentUploaded State = "identity_document_uploaded"
	BiometricCaptureUploaded State = "biometric_capture_uploaded"
	VerificationPending      State = "verification_pending"
	Verified                 State = "verified"
	Rejected                 State = "rejected"
	Expired                  State = "expired"
)

func (s State) String() string { return string(s) }

func ParseState(value string) (State, error) {
	state := State(value)
	switch state {
	case Created,
		PersonalDetailsSubmitted,
		IdentityDocumentUploaded,
		BiometricCaptureUploaded,
		VerificationPending,
		Verified,
		Rejected,
		Expired:
		return state, nil
	default:
		return "", fmt.Errorf("unknown Verification Session state %q", value)
	}
}

func Transition(current State, action sessionevent.Type) (State, error) {
	if isTerminal(current) {
		return "", fmt.Errorf("%w: %q", ErrTerminalState, current)
	}

	allowed := map[State]map[sessionevent.Type]State{
		Created: {
			sessionevent.SubmitPersonalDetails: PersonalDetailsSubmitted,
		},
		PersonalDetailsSubmitted: {
			sessionevent.ConfirmIdentityDocument: IdentityDocumentUploaded,
		},
		IdentityDocumentUploaded: {
			sessionevent.ConfirmBiometricCapture: BiometricCaptureUploaded,
		},
		BiometricCaptureUploaded: {
			sessionevent.SubmitSession: VerificationPending,
		},
		VerificationPending: {
			sessionevent.VerificationPassed: Verified,
			sessionevent.VerificationFailed: Rejected,
		},
	}

	next, ok := allowed[current][action]
	if !ok {
		return "", fmt.Errorf("%w: action %q from state %q", ErrTransitionForbidden, action, current)
	}
	return next, nil
}

func Expire(current State, now, deadline time.Time) (State, error) {
	if isTerminal(current) {
		return "", fmt.Errorf("%w: %q", ErrTerminalState, current)
	}
	if now.Before(deadline) {
		return "", ErrDeadlineNotReached
	}
	return Expired, nil
}

func isTerminal(state State) bool {
	return state == Verified || state == Rejected || state == Expired
}
