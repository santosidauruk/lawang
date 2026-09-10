package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/fakeprovider"
)

const (
	fakeSubmissionBodyLimit = 1 << 20
)

type providerSubmissionRequest struct {
	SessionID        *uuid.UUID                                 `json:"sessionId"`
	CallbackURL      *string                                    `json:"callbackUrl"`
	PersonalDetails  *fakeprovider.PersonalDetails              `json:"personalDetails"`
	IdentityDocument *fakeprovider.VerificationArtifactMetadata `json:"identityDocument"`
	BiometricCapture *fakeprovider.VerificationArtifactMetadata `json:"biometricCapture"`
}

type FakeProviderScenarioStore interface {
	Set(sessionID uuid.UUID, scenario fakeprovider.Scenario)
}

type FakeSubmissionService interface {
	Accept(ctx context.Context, idempotencyKey uuid.UUID, request fakeprovider.ProviderSubmissionRequest) (bool, error)
}

func NewFakeProviderScenarioHandler(fakeSubmissionService FakeSubmissionService, scenarios FakeProviderScenarioStore) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", requireMethod(http.MethodGet, liveHealth))
	if fakeSubmissionService != nil {
		mux.HandleFunc("/{$}", requireMethod(http.MethodPost, handleFakeProviderSubmission(fakeSubmissionService)))
	}
	if scenarios != nil {
		mux.HandleFunc("/test/scenarios/{sessionId}", requireMethod(http.MethodPut, handleFakeProviderScenario(scenarios)))
	}
	return mux
}

func handleFakeProviderSubmission(service FakeSubmissionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		idempotencyKey, err := uuid.Parse(request.Header.Get("Idempotency-Key"))
		if err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{
				Code: "VALIDATION_ERROR", Message: "idempotency must be a UUID",
			})
			return
		}

		var body providerSubmissionRequest
		if !decodeFakeProviderJSON(response, request, &body, "invalid provider submission request") {
			return
		}

		if body.SessionID == nil || body.CallbackURL == nil || body.PersonalDetails == nil || body.IdentityDocument == nil || body.BiometricCapture == nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "all provider submission fields are required"})
			return
		}

		if idempotencyKey != *body.SessionID {
			writeJSON(response, http.StatusBadRequest, APIError{
				Code: "VALIDATION_ERROR", Message: "idempotency mismatch",
			})
			return
		}

		payload := fakeprovider.ProviderSubmissionRequest{
			SessionID:        *body.SessionID,
			CallbackURL:      *body.CallbackURL,
			PersonalDetails:  *body.PersonalDetails,
			IdentityDocument: *body.IdentityDocument,
			BiometricCapture: *body.BiometricCapture,
		}
		_, err = service.Accept(request.Context(), *body.SessionID, payload)
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
			return
		}

		writeJSON(response, http.StatusOK, struct {
			Status string `json:"status"`
		}{
			Status: "ok",
		})
	}
}

type scenarioRequest struct {
	Verdict            *fakeprovider.Verdict         `json:"verdict"`
	Reason             *fakeprovider.RejectionReason `json:"reason,omitempty"`
	DelayMs            *int                          `json:"delayMs"`
	DuplicateCallbacks *int                          `json:"duplicateCallbacks"`
}

func handleFakeProviderScenario(scenarios FakeProviderScenarioStore) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		sessionID, err := uuid.Parse(request.PathValue("sessionId"))
		if err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "id must be a UUID"})
			return
		}

		var body scenarioRequest
		if !decodeFakeProviderJSON(response, request, &body, "invalid fake provider scenario request") {
			return
		}

		if body.Verdict == nil || body.DelayMs == nil || body.DuplicateCallbacks == nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "all required scenario fields must be provided"})
			return
		}
		var reason fakeprovider.RejectionReason
		if body.Reason != nil {
			reason = *body.Reason
		}

		scenario := fakeprovider.Scenario{
			Verdict:            *body.Verdict,
			Reason:             reason,
			DelayMs:            *body.DelayMs,
			DuplicateCallbacks: *body.DuplicateCallbacks,
		}
		if err := scenario.Validate(); err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "invalid fake provider scenario"})
			return
		}

		scenarios.Set(sessionID, scenario)
	}

}

func decodeFakeProviderJSON(response http.ResponseWriter, request *http.Request, destination any, invalidMessage string) bool {
	request.Body = http.MaxBytesReader(response, request.Body, fakeSubmissionBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeFakeProviderDecodeError(response, err, invalidMessage)
		return false
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: invalidMessage})
		} else {
			writeFakeProviderDecodeError(response, err, invalidMessage)
		}
		return false
	}
	return true
}

func writeFakeProviderDecodeError(response http.ResponseWriter, err error, invalidMessage string) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeJSON(response, http.StatusRequestEntityTooLarge, APIError{
			Code: "PAYLOAD_TOO_LARGE", Message: "request body exceeds 1 MiB limit",
		})
		return
	}
	var typeError *json.UnmarshalTypeError
	var syntaxError *json.SyntaxError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &typeError) || errors.As(err, &syntaxError) || strings.HasPrefix(err.Error(), "json: unknown field ") {
		writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: invalidMessage})
		return
	}
	writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
}
