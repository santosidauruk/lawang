package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type SessionService interface {
	Create(context.Context) (session.CreatedSession, error)
	Resume(context.Context, uuid.UUID, string) (session.Summary, error)
}

// NewHandler builds the public HTTP routing surface.
func NewHandler(sessionService SessionService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", liveHealth)
	if sessionService != nil {
		mux.HandleFunc("POST /verification-sessions", createVerificationSession(sessionService))
		mux.HandleFunc("GET /verification-sessions/{id}", resumeVerificationSession(sessionService))
	}
	return mux
}

func resumeVerificationSession(service SessionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		token, authError := ParseBearer(request.Header.Get("Authorization"))
		if authError != nil {
			writeJSON(response, http.StatusUnauthorized, authError)
			return
		}

		id, err := uuid.Parse(request.PathValue("id"))
		if err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{
				Code: "VALIDATION_ERROR", Message: "id must be a UUID",
			})
			return
		}
		summary, err := service.Resume(request.Context(), id, token)
		if err != nil {
			writeSessionError(response, id, err)
			return
		}

		writeJSON(response, http.StatusOK, struct {
			ID        string         `json:"id"`
			Status    session.Status `json:"status"`
			ExpiresAt string         `json:"expiresAt"`
		}{
			ID: summary.ID.String(), Status: summary.Status,
			ExpiresAt: summary.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
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

func liveHealth(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("{\"status\":\"ok\"}\n"))
}

func createVerificationSession(service SessionService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		created, err := service.Create(request.Context())
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, APIError{
				Code: "INTERNAL", Message: "internal server error",
			})
			return
		}
		writeJSON(response, http.StatusCreated, struct {
			ID          string         `json:"id"`
			Status      session.Status `json:"status"`
			ExpiresAt   string         `json:"expiresAt"`
			ResumeToken string         `json:"resumeToken"`
		}{
			ID: created.ID.String(), Status: created.Status,
			ExpiresAt: created.ExpiresAt.UTC().Format(time.RFC3339), ResumeToken: created.ResumeToken,
		})
	}
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}
