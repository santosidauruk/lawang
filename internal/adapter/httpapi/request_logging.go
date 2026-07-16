package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// WithRequestLogging records one redacted completion event for every HTTP request.
func WithRequestLogging(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := uuid.NewString()
		response.Header().Set("X-Request-ID", requestID)
		outcome := &responseOutcome{ResponseWriter: response}

		next.ServeHTTP(outcome, request)

		status := outcome.status
		if status == 0 {
			status = http.StatusOK
		}
		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}
		logger.Info("HTTP request completed",
			"request_id", requestID,
			"method", request.Method,
			"route", route,
			"status", status,
			"duration_ms", float64(time.Since(startedAt))/float64(time.Millisecond),
			"error_code", outcome.errorCode,
		)
	})
}

type responseOutcome struct {
	http.ResponseWriter
	status    int
	errorCode string
}

func (response *responseOutcome) WriteHeader(status int) {
	if response.status != 0 {
		return
	}
	response.status = status
	response.ResponseWriter.WriteHeader(status)
}

func (response *responseOutcome) Write(body []byte) (int, error) {
	if response.status == 0 {
		response.WriteHeader(http.StatusOK)
	}
	return response.ResponseWriter.Write(body)
}

func (response *responseOutcome) recordErrorCode(code string) {
	response.errorCode = code
}
