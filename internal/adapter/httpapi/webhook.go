package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	webhookBodyLimit = 1 << 20
	sha256MACLength  = sha256.Size
)

func writeInvalidWebhookSignature(response http.ResponseWriter) {
	writeJSON(response, http.StatusUnauthorized, APIError{
		Code: "INVALID_SIGNATURE", Message: "invalid webhook signature",
	})
}

func SetHmacSubmissionBody(secretKey string, message []byte) ([]byte, error) {
	key := []byte(secretKey)

	h := hmac.New(sha256.New, key)

	_, err := h.Write(message)
	if err != nil {
		return []byte(nil), err
	}

	hashBytes := h.Sum(nil)

	return hashBytes, nil
}

func verifyProviderSubmission(service VerifiedBodyService, providerWebhookSecret string) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(response, request.Body, webhookBodyLimit)
		bytesBody, err := io.ReadAll(request.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeJSON(response, http.StatusRequestEntityTooLarge, APIError{
					Code: "PAYLOAD_TOO_LARGE", Message: "request body exceeds 1 MiB limit",
				})
				return
			}
			writeJSON(response, http.StatusInternalServerError, APIError{
				Code: "INTERNAL", Message: "internal server error",
			})
			return
		}

		signatureHeader := request.Header.Get("x-signature")
		if !strings.HasPrefix(signatureHeader, "sha256=") {
			writeInvalidWebhookSignature(response)
			return
		}

		decodedHmac, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, "sha256="))
		if err != nil || len(decodedHmac) != sha256MACLength {
			writeInvalidWebhookSignature(response)
			return
		}

		bodyHmac, err := SetHmacSubmissionBody(providerWebhookSecret, bytesBody)
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, APIError{
				Code: "INTERNAL", Message: "internal server error",
			})
			return
		}
		if !hmac.Equal(bodyHmac, decodedHmac) {
			writeInvalidWebhookSignature(response)
			return
		}

		ctx := request.Context()
		applyInput, err := service.HandleVerifiedBody(ctx, bytesBody)
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, APIError{
				Code: "INTERNAL", Message: "internal server error",
			})
			return
		}

		err = service.Apply(ctx, applyInput)
		if err != nil {
			writeJSON(response, http.StatusInternalServerError, APIError{
				Code: "INTERNAL", Message: "internal server error",
			})
			return
		}

		writeJSON(response, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "ok"})
	}
}
