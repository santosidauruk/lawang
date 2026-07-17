package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type SessionService interface {
	Create(context.Context) (session.CreatedSession, error)
	Resume(context.Context, uuid.UUID, string) (session.Summary, error)
}

type PersonalDetailsService interface {
	Submit(context.Context, uuid.UUID, string, personaldetails.Input) (session.Summary, error)
}

// NewHandler builds the public HTTP routing surface.
func NewHandler(sessionService SessionService, personalDetailsService PersonalDetailsService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", requireMethod(http.MethodGet, liveHealth))
	if sessionService != nil {
		mux.HandleFunc("/verification-sessions", requireMethod(http.MethodPost, createVerificationSession(sessionService)))
		mux.HandleFunc("/verification-sessions/{id}", requireMethod(http.MethodGet, resumeVerificationSession(sessionService)))
	}
	if personalDetailsService != nil {
		mux.HandleFunc(
			"/verification-sessions/{id}/personal-details",
			requireMethod(http.MethodPost, submitPersonalDetails(personalDetailsService)),
		)
	}
	return mux
}

type personalDetailsRequest struct {
	FullName       *string `json:"fullName"`
	DateOfBirth    *string `json:"dateOfBirth"`
	IdentityNumber *string `json:"identityNumber"`
	Address        *string `json:"address"`
}

func submitPersonalDetails(service PersonalDetailsService) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		token, authError := ParseBearer(request.Header.Get("Authorization"))
		if authError != nil {
			writeJSON(response, http.StatusUnauthorized, authError)
			return
		}
		id, err := uuid.Parse(request.PathValue("id"))
		if err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "id must be a UUID"})
			return
		}
		var body personalDetailsRequest
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
		if body.FullName == nil || body.DateOfBirth == nil || body.IdentityNumber == nil || body.Address == nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "all Personal Details fields are required"})
			return
		}
		dateOfBirth, err := time.Parse("2006-01-02", *body.DateOfBirth)
		if err != nil {
			writeJSON(response, http.StatusBadRequest, APIError{Code: "VALIDATION_ERROR", Message: "dateOfBirth must be an ISO calendar date"})
			return
		}
		summary, err := service.Submit(request.Context(), id, token, personaldetails.Input{
			FullName: *body.FullName, DateOfBirth: dateOfBirth,
			IdentityNumber: *body.IdentityNumber, Address: *body.Address,
		})
		if err != nil {
			writePersonalDetailsError(response, id, err)
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
