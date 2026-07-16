package httpapi

import "net/http"

func requireMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Method == method || (method == http.MethodGet && request.Method == http.MethodHead) {
			next(response, request)
			return
		}

		allowed := method
		if method == http.MethodGet {
			allowed += ", " + http.MethodHead
		}
		response.Header().Set("Allow", allowed)
		writeJSON(response, http.StatusMethodNotAllowed, APIError{
			Code: "METHOD_NOT_ALLOWED", Message: "method not allowed",
		})
	}
}
