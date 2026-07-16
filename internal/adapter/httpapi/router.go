package httpapi

import (
	"context"
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
	mux.HandleFunc("/health/live", requireMethod(http.MethodGet, liveHealth))
	if sessionService != nil {
		mux.HandleFunc("/verification-sessions", requireMethod(http.MethodPost, createVerificationSession(sessionService)))
		mux.HandleFunc("/verification-sessions/{id}", requireMethod(http.MethodGet, resumeVerificationSession(sessionService)))
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
