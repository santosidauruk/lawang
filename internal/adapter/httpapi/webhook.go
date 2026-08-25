package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
)

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
