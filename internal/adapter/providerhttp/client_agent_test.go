package providerhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestProviderClientClassifiesStatusAndUsesStableKey(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		status        int
		expectedError error
	}{
		{name: "accepted", status: http.StatusAccepted},
		{name: "request timeout", status: http.StatusRequestTimeout, expectedError: providersubmission.ErrTransientProvider},
		{name: "too many requests", status: http.StatusTooManyRequests, expectedError: providersubmission.ErrTransientProvider},
		{name: "server failure", status: http.StatusServiceUnavailable, expectedError: providersubmission.ErrTransientProvider},
		{name: "bad request", status: http.StatusBadRequest, expectedError: providersubmission.ErrPermanentProvider},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			sessionID := uuid.New()
			client, err := NewClient("http://provider.internal/submit", time.Second)
			if err != nil {
				t.Fatalf("new provider client: %v", err)
			}
			client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.Header.Get("Idempotency-Key"); got != sessionID.String() {
					t.Errorf("expected stable session idempotency key %q, got %q", sessionID, got)
				}
				return &http.Response{
					StatusCode: testCase.status,
					Body:       io.NopCloser(strings.NewReader("response")),
					Header:     make(http.Header),
				}, nil
			})
			err = client.Send(context.Background(), providersubmission.ProviderSubmissionRequest{SessionID: sessionID})
			if !errors.Is(err, testCase.expectedError) {
				t.Fatalf("expected %v, got %v", testCase.expectedError, err)
			}
		})
	}
}

func TestProviderClientClassifiesNetworkFailureAsTransient(t *testing.T) {
	t.Parallel()

	client, err := NewClient("http://provider.internal/submit", time.Second)
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	client.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	err = client.Send(context.Background(), providersubmission.ProviderSubmissionRequest{SessionID: uuid.New()})
	if !errors.Is(err, providersubmission.ErrTransientProvider) {
		t.Fatalf("expected transient network failure, got %v", err)
	}
}

func TestProviderClientPreservesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client, err := NewClient("http://provider.internal/submit", time.Second)
	if err != nil {
		t.Fatalf("new provider client: %v", err)
	}
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, request.Context().Err()
	})

	err = client.Send(ctx, providersubmission.ProviderSubmissionRequest{SessionID: uuid.New()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
