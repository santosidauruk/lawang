package verificationsession_test

import (
	"errors"
	"testing"
	"time"

	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

func TestTransitionFollowsRequiredForwardPath(t *testing.T) {
	tests := []struct {
		name    string
		current verificationsession.State
		action  sessionevent.Type
		want    verificationsession.State
	}{
		{"personal details", verificationsession.Created, sessionevent.SubmitPersonalDetails, verificationsession.PersonalDetailsSubmitted},
		{"identity document", verificationsession.PersonalDetailsSubmitted, sessionevent.ConfirmIdentityDocument, verificationsession.IdentityDocumentUploaded},
		{"biometric capture", verificationsession.IdentityDocumentUploaded, sessionevent.ConfirmBiometricCapture, verificationsession.BiometricCaptureUploaded},
		{"provider submission", verificationsession.BiometricCaptureUploaded, sessionevent.SubmitSession, verificationsession.VerificationPending},
		{"provider passed", verificationsession.VerificationPending, sessionevent.VerificationPassed, verificationsession.Verified},
		{"provider failed", verificationsession.VerificationPending, sessionevent.VerificationFailed, verificationsession.Rejected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verificationsession.Transition(tt.current, tt.action)
			if err != nil {
				t.Fatalf("Transition(%q, %q) error = %v", tt.current, tt.action, err)
			}
			if got != tt.want {
				t.Errorf("Transition(%q, %q) = %q, want %q", tt.current, tt.action, got, tt.want)
			}
		})
	}
}

func TestParseStateAdmitsExactlyThePublicStateStrings(t *testing.T) {
	states := []verificationsession.State{
		verificationsession.Created,
		verificationsession.PersonalDetailsSubmitted,
		verificationsession.IdentityDocumentUploaded,
		verificationsession.BiometricCaptureUploaded,
		verificationsession.VerificationPending,
		verificationsession.Verified,
		verificationsession.Rejected,
		verificationsession.Expired,
	}
	for _, want := range states {
		got, err := verificationsession.ParseState(want.String())
		if err != nil || got != want {
			t.Errorf("ParseState(%q) = %q, %v; want %q, nil", want, got, err, want)
		}
	}
	if _, err := verificationsession.ParseState("submit_personal_details"); err == nil {
		t.Fatal("ParseState accepted a Session Event action verb")
	}
	if _, err := verificationsession.ParseState("unknown"); err == nil {
		t.Fatal("ParseState accepted an unknown state")
	}
}

func TestExpireRequiresApplicableDeadlineAndNonTerminalState(t *testing.T) {
	deadline := time.Date(2026, 7, 16, 10, 30, 0, 0, time.UTC)
	nonTerminalStates := []verificationsession.State{
		verificationsession.Created,
		verificationsession.PersonalDetailsSubmitted,
		verificationsession.IdentityDocumentUploaded,
		verificationsession.BiometricCaptureUploaded,
		verificationsession.VerificationPending,
	}

	for _, current := range nonTerminalStates {
		t.Run(string(current)+" before deadline", func(t *testing.T) {
			_, err := verificationsession.Expire(current, deadline.Add(-time.Nanosecond), deadline)
			if !errors.Is(err, verificationsession.ErrDeadlineNotReached) {
				t.Fatalf("Expire(%q) error = %v, want ErrDeadlineNotReached", current, err)
			}
		})
		t.Run(string(current)+" at deadline", func(t *testing.T) {
			got, err := verificationsession.Expire(current, deadline, deadline)
			if err != nil {
				t.Fatalf("Expire(%q) error = %v", current, err)
			}
			if got != verificationsession.Expired {
				t.Errorf("Expire(%q) = %q, want %q", current, got, verificationsession.Expired)
			}
		})
	}

	for _, terminal := range []verificationsession.State{
		verificationsession.Verified,
		verificationsession.Rejected,
		verificationsession.Expired,
	} {
		_, err := verificationsession.Expire(terminal, deadline, deadline)
		if !errors.Is(err, verificationsession.ErrTerminalState) {
			t.Errorf("Expire(%q) error = %v, want ErrTerminalState", terminal, err)
		}
	}
}

func TestTerminalStatesNeverTransition(t *testing.T) {
	actions := []sessionevent.Type{
		sessionevent.SubmitPersonalDetails,
		sessionevent.ConfirmIdentityDocument,
		sessionevent.ConfirmBiometricCapture,
		sessionevent.SubmitSession,
		sessionevent.VerificationPassed,
		sessionevent.VerificationFailed,
	}
	for _, terminal := range []verificationsession.State{
		verificationsession.Verified,
		verificationsession.Rejected,
		verificationsession.Expired,
	} {
		for _, action := range actions {
			t.Run(string(terminal)+"/"+action.String(), func(t *testing.T) {
				_, err := verificationsession.Transition(terminal, action)
				if !errors.Is(err, verificationsession.ErrTerminalState) {
					t.Fatalf("Transition(%q, %q) error = %v, want ErrTerminalState", terminal, action, err)
				}
			})
		}
	}
}

func TestEveryOtherForwardTransitionIsForbidden(t *testing.T) {
	allowed := map[verificationsession.State]sessionevent.Type{
		verificationsession.Created:                  sessionevent.SubmitPersonalDetails,
		verificationsession.PersonalDetailsSubmitted: sessionevent.ConfirmIdentityDocument,
		verificationsession.IdentityDocumentUploaded: sessionevent.ConfirmBiometricCapture,
		verificationsession.BiometricCaptureUploaded: sessionevent.SubmitSession,
	}
	states := []verificationsession.State{
		verificationsession.Created,
		verificationsession.PersonalDetailsSubmitted,
		verificationsession.IdentityDocumentUploaded,
		verificationsession.BiometricCaptureUploaded,
		verificationsession.VerificationPending,
	}
	actions := []sessionevent.Type{
		sessionevent.SubmitPersonalDetails,
		sessionevent.ConfirmIdentityDocument,
		sessionevent.ConfirmBiometricCapture,
		sessionevent.SubmitSession,
		sessionevent.VerificationPassed,
		sessionevent.VerificationFailed,
	}

	for _, current := range states {
		for _, action := range actions {
			if action == allowed[current] ||
				current == verificationsession.VerificationPending &&
					(action == sessionevent.VerificationPassed || action == sessionevent.VerificationFailed) {
				continue
			}
			_, err := verificationsession.Transition(current, action)
			if !errors.Is(err, verificationsession.ErrTransitionForbidden) {
				t.Errorf("Transition(%q, %q) error = %v, want ErrTransitionForbidden", current, action, err)
			}
		}
	}
}
