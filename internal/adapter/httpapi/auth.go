package httpapi

import "regexp"

var bearerPattern = regexp.MustCompile(`(?i)^Bearer[ \t]+(\S+)$`)

func ParseBearer(authorization string) (string, *APIError) {
	if authorization == "" {
		return "", &APIError{
			Code:    "MISSING_AUTHORIZATION",
			Message: "Auth header required",
		}
	}

	match := bearerPattern.FindStringSubmatch(authorization)
	if match == nil {
		return "", &APIError{
			Code:    "MALFORMED_AUTHORIZATION",
			Message: "expected Bearer <token>",
		}
	}
	return match[1], nil
}
