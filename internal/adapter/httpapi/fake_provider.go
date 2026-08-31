package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
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
	if fakeSubmissionService != nil {
		mux.HandleFunc("/", requireMethod(http.MethodPost, handleFakeProviderSubmission(fakeSubmissionService)))
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
		request.Body = http.MaxBytesReader(response, request.Body, fakeSubmissionBodyLimit)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()

		if err := decoder.Decode(&body); err != nil {
			var typeError *json.UnmarshalTypeError
			if errors.As(err, &typeError) || strings.HasPrefix(err.Error(), "json: unknown field ") {
				writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "invalid Personal Details request"})
				return
			}
			writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
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
	Reason             *fakeprovider.RejectionReason `json:"reason"`
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
		request.Body = http.MaxBytesReader(response, request.Body, 1<<20)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			var typeError *json.UnmarshalTypeError
			if errors.As(err, &typeError) || strings.HasPrefix(err.Error(), "json: unknown field ") {
				writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "invalid Personal Details request"})
				return
			}
			writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "Internal server error"})
			return
		}

		if body.Verdict == nil || body.Reason == nil || body.DelayMs == nil || body.DuplicateCallbacks == nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "all Personal Details fields are required"})
			return
		}

		scenario := fakeprovider.Scenario{
			Verdict:            *body.Verdict,
			Reason:             *body.Reason,
			DelayMs:            *body.DelayMs,
			DuplicateCallbacks: *body.DuplicateCallbacks,
		}

		scenarios.Set(sessionID, scenario)
	}

}
