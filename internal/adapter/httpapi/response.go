package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeSessionError(response http.ResponseWriter, id uuid.UUID, err error) {
	var serviceError *session.Error
	if !errors.As(err, &serviceError) {
		writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "internal server error"})
		return
	}

	details := map[string]any{"id": id.String()}
	switch serviceError.Code {
	case session.CodeSessionNotFound:
		writeJSON(response, http.StatusNotFound, APIError{
			Code: string(serviceError.Code), Message: "session " + id.String() + " not found", Details: details,
		})
	case session.CodeInvalidResumeToken:
		writeJSON(response, http.StatusUnauthorized, APIError{
			Code: string(serviceError.Code), Message: "invalid resume token", Details: details,
		})
	case session.CodeSessionExpired:
		writeJSON(response, http.StatusGone, APIError{
			Code: string(serviceError.Code), Message: "verification session expired", Details: details,
		})
	default:
		writeJSON(response, http.StatusInternalServerError, APIError{Code: "INTERNAL", Message: "internal server error"})
	}
}

func writePersonalDetailsError(response http.ResponseWriter, id uuid.UUID, err error) {
	var serviceError *personaldetails.Error
	if errors.As(err, &serviceError) {
		switch serviceError.Code {
		case personaldetails.CodeConflict:
			writeJSON(response, http.StatusConflict, APIError{
				Code:    string(serviceError.Code),
				Message: "personal details already submitted with different values for session " + serviceError.ID.String(),
				Details: map[string]any{"id": serviceError.ID.String()},
			})
			return
		case personaldetails.CodeIllegalTransition:
			writeJSON(response, http.StatusConflict, APIError{
				Code:    string(serviceError.Code),
				Message: "illegal transition from " + serviceError.From.String() + " via " + serviceError.Action.String(),
				Details: map[string]any{
					"id": serviceError.ID.String(), "from": serviceError.From.String(), "event": serviceError.Action.String(),
				},
			})
			return
		}
	}
	writeSessionError(response, id, err)
}

func writeProviderSubmissionError(response http.ResponseWriter, id uuid.UUID, err error) {
	var submissionError *providersubmission.Error
	if !errors.As(err, &submissionError) {
		writeSessionError(response, id, err)
		return
	}

	details := map[string]any{"id": submissionError.ID.String()}
	switch submissionError.Code {
	case providersubmission.CodeSubmissionNotReady:
		writeJSON(response, http.StatusConflict, APIError{
			Code:    string(submissionError.Code),
			Message: "verification session is not ready for provider submission",
			Details: details,
		})
	case providersubmission.CodeIllegalTransition:
		details["from"] = submissionError.From.String()
		details["event"] = submissionError.Action.String()
		writeJSON(response, http.StatusConflict, APIError{
			Code:    string(submissionError.Code),
			Message: "illegal transition from " + submissionError.From.String() + " via " + submissionError.Action.String(),
			Details: details,
		})
	default:
		writeJSON(response, http.StatusInternalServerError, APIError{
			Code: "INTERNAL", Message: "internal server error",
		})
	}
}

func writeArtifactError(response http.ResponseWriter, id uuid.UUID, err error) {
	var artifactError *artifact.Error
	if !errors.As(err, &artifactError) {
		writeSessionError(response, id, err)
		return
	}

	switch artifactError.Code {
	case artifact.CodeLocalValidationFailed:
		writeJSON(response, http.StatusUnprocessableEntity, APIError{
			Code:    string(artifactError.Code),
			Message: "The uploaded identity document did not match the submitted details",
			Details: boundedArtifactFailureDetails(artifactError),
		})
	case artifact.CodeUploadIntentNotFound:
		writeJSON(response, http.StatusNotFound, APIError{
			Code: string(artifactError.Code), Message: "upload intent not found",
		})
	case artifact.CodeUploadIntentExpired:
		writeJSON(response, http.StatusConflict, APIError{
			Code: string(artifactError.Code), Message: "upload intent expired",
		})
	case artifact.CodeUploadIntentSuperseded:
		writeJSON(response, http.StatusConflict, APIError{
			Code: string(artifactError.Code), Message: "upload intent was superseded",
		})
	case artifact.CodeInvalidUploadIntentKind:
		writeJSON(response, http.StatusConflict, APIError{
			Code:    string(artifactError.Code),
			Message: "upload intent kind is not valid for artifact confirmation",
		})
	case artifact.CodeInvalidObjectMetadata:
		writeJSON(response, http.StatusUnprocessableEntity, APIError{
			Code:    string(artifactError.Code),
			Message: "uploaded object metadata is invalid",
			Details: boundedArtifactFailureDetails(artifactError),
		})
	case artifact.CodeConfirmationStale:
		writeJSON(response, http.StatusConflict, APIError{
			Code:    string(artifactError.Code),
			Message: "artifact confirmation state changed; retry the request",
		})
	case artifact.CodeUploadIntentStale:
		writeJSON(response, http.StatusConflict, APIError{
			Code:    string(artifactError.Code),
			Message: "upload intent state changed; retry the request",
		})
	case artifact.CodeObjectStorageFailed:
		writeJSON(response, http.StatusServiceUnavailable, APIError{
			Code:    string(artifactError.Code),
			Message: "object storage is temporarily unavailable",
		})
	case artifact.CodeDocumentExtractionFailed:
		writeJSON(response, http.StatusInternalServerError, APIError{
			Code:    string(artifactError.Code),
			Message: "identity document processing failed",
		})
	default:
		writeJSON(response, http.StatusInternalServerError, APIError{
			Code: "INTERNAL", Message: "internal server error",
		})
	}
}

func writeArtifactUploadIntentError(response http.ResponseWriter, id uuid.UUID, err error) {
	var artifactError *artifact.Error
	if errors.As(err, &artifactError) &&
		artifactError.Code == artifact.CodeInvalidUploadIntentKind {
		writeJSON(response, http.StatusBadRequest, APIError{
			Code:    string(artifactError.Code),
			Message: "only identity_document and biometric_capture uploads are supported",
		})
		return
	}
	writeArtifactError(response, id, err)
}

func boundedArtifactFailureDetails(artifactError *artifact.Error) map[string]any {
	switch {
	case artifactError.Code == artifact.CodeLocalValidationFailed &&
		artifactError.Reason == artifact.ReasonIdentityNumberMismatch:
		return map[string]any{"reason": string(artifactError.Reason)}
	case artifactError.Code == artifact.CodeInvalidObjectMetadata &&
		(artifactError.Reason == artifact.ReasonObjectEmpty ||
			artifactError.Reason == artifact.ReasonObjectTooLarge ||
			artifactError.Reason == artifact.ReasonUnsupportedContentType):
		return map[string]any{"reason": string(artifactError.Reason)}
	default:
		return nil
	}
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	var errorCode string
	switch apiError := body.(type) {
	case APIError:
		errorCode = apiError.Code
	case *APIError:
		if apiError != nil {
			errorCode = apiError.Code
		}
	}
	if errorCode != "" {
		if recorder, ok := response.(interface{ recordErrorCode(string) }); ok {
			recorder.recordErrorCode(errorCode)
		}
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}
