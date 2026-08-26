package httpapi_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
)

const checkpoint2WebhookSecret = "checkpoint-2-fixed-webhook-secret"

var checkpoint2ExactWebhookBody = []byte(
	`{"eventId":"8d4bf9ce-5c57-4e88-99bf-f17cf403bc36","sessionId":"28c993b3-fc05-46aa-b453-0bace6c34572","verdict":"verified"}`,
)

// recordingVerifiedBodyService is the HTTP-boundary recorder for the Checkpoint 2
// learning gate. It deliberately does not decode or otherwise interpret the body.
// The user-authored tests and handler decide whether bytes reach this boundary.
type recordingVerifiedBodyService struct {
	calls  int
	bodies [][]byte
}

func (service *recordingVerifiedBodyService) HandleVerifiedBody(_ context.Context, body []byte) error {
	service.calls++
	service.bodies = append(service.bodies, append([]byte(nil), body...))
	return nil
}

func TestWebhookForwardExactRawBodyWithValidSignature(t *testing.T) {
	service := &recordingVerifiedBodyService{}
	verifiedBody := httpapi.VerifiedBody{
		Service:               service,
		ProviderWebhookSecret: checkpoint2WebhookSecret,
	}

	signature, err := setHmacVerifiedBody(checkpoint2WebhookSecret, checkpoint2ExactWebhookBody)
	if err != nil {
		t.Fatalf("SetHmacSubmissionBody: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(checkpoint2ExactWebhookBody))
	request.Header.Set("x-signature", fmt.Sprintf("sha256=%s", signature))

	response := httptest.NewRecorder()
	httpapi.NewHandler(nil, nil, nil, nil, &verifiedBody, nil).ServeHTTP(response, request)

	if service.calls != 1 {
		t.Fatalf("service calls got %d, want 1", service.calls)
	}
	if !bytes.Equal(service.bodies[0], checkpoint2ExactWebhookBody) {
		t.Errorf("raw body is not the same with signature")
	}

	responseStatusCode := response.Result().StatusCode
	if responseStatusCode != http.StatusOK {
		t.Errorf("response status code got: %d, want 200", responseStatusCode)
	}

}

func TestWebhookChangedWhitespaceWithOriginalSignature(t *testing.T) {
	service := &recordingVerifiedBodyService{}
	verifiedBody := httpapi.VerifiedBody{
		Service:               service,
		ProviderWebhookSecret: checkpoint2WebhookSecret,
	}

	signature, err := setHmacVerifiedBody(checkpoint2WebhookSecret, checkpoint2ExactWebhookBody)
	if err != nil {
		t.Fatalf("SetHmacSubmissionBody: %v", err)
	}

	changedBody := append(append([]byte(nil), checkpoint2ExactWebhookBody...), ' ')
	request := httptest.NewRequest(http.MethodPost, "/webhooks/verification", bytes.NewReader(changedBody))
	request.Header.Set("x-signature", fmt.Sprintf("sha256=%s", signature))

	response := httptest.NewRecorder()
	httpapi.NewHandler(nil, nil, nil, nil, &verifiedBody, nil).ServeHTTP(response, request)

	if service.calls != 0 {
		t.Fatalf("service calls got %d, want 0", service.calls)
	}

	responseStatusCode := response.Result().StatusCode
	if responseStatusCode != http.StatusUnauthorized {
		t.Errorf("response status code got %d, want 401", responseStatusCode)
	}
}

func setHmacVerifiedBody(secretKey string, message []byte) (string, error) {
	key := []byte(secretKey)

	h := hmac.New(sha256.New, key)

	_, err := h.Write(message)
	if err != nil {
		return "", err
	}

	hashBytes := h.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

	return hashString, nil
}
