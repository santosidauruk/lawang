package providerhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/fakeprovider"
)

var (
	ErrCallbackNonSuccess = errors.New("callback returned non-success status")
	ErrCallbackTransport  = errors.New("callback transport failed")
	ErrCallbackRequest    = errors.New("callback request is invalid")
)

type ScenarioStore struct {
	mu              sync.Mutex
	defaultScenario fakeprovider.Scenario
	bySessionID     map[uuid.UUID]fakeprovider.Scenario
}

type CallbackSender struct {
	secretKey string
}

func NewScenarioStore() *ScenarioStore {
	return &ScenarioStore{
		defaultScenario: fakeprovider.Scenario{
			Verdict:            fakeprovider.Verified,
			Reason:             "",
			DelayMs:            0,
			DuplicateCallbacks: 0,
		},
		bySessionID: map[uuid.UUID]fakeprovider.Scenario{},
	}
}

func NewCallbackSender(secretKey string) *CallbackSender {
	return &CallbackSender{
		secretKey: secretKey,
	}
}

func (s *ScenarioStore) Lookup(sessionID uuid.UUID) fakeprovider.Scenario {
	s.mu.Lock()
	defer s.mu.Unlock()
	scenario, found := s.bySessionID[sessionID]
	if found {
		return scenario
	}
	return s.defaultScenario
}
func (s *ScenarioStore) Set(sessionID uuid.UUID, scenario fakeprovider.Scenario) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bySessionID[sessionID] = scenario
}

func (c *CallbackSender) Send(ctx context.Context, callbackURL string, event fakeprovider.WebhookEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	mac, err := setHmacVerifiedBody(c.secretKey, body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return ErrCallbackRequest
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-signature", "sha256="+hex.EncodeToString(mac))

	client := http.Client{}
	resp, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return ErrCallbackTransport
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ErrCallbackNonSuccess
	}
	return nil
}

func setHmacVerifiedBody(secretKey string, message []byte) ([]byte, error) {
	key := []byte(secretKey)

	h := hmac.New(sha256.New, key)

	_, err := h.Write(message)
	if err != nil {
		return []byte(nil), err
	}

	hashBytes := h.Sum(nil)

	return hashBytes, nil
}
