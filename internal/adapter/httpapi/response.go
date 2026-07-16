package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
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
