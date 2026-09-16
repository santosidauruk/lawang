package providerhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
)

const maxProviderResponseBytes = 64 << 10

type Client struct {
	baseURL string
	client  *http.Client
}

func NewClient(baseURL string, timeout time.Duration) (*Client, error) {
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%w: provider base URL", providersubmission.ErrPermanentProvider)
	}

	if timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout invalid", providersubmission.ErrPermanentProvider)
	}
	return &Client{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) Send(ctx context.Context, submission providersubmission.ProviderSubmissionRequest) error {
	body, err := json.Marshal(submission)
	if err != nil {
		return fmt.Errorf("%w: encode request", providersubmission.ErrPermanentProvider)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: construct request", providersubmission.ErrPermanentProvider)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", submission.SessionID.String())

	response, err := c.client.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}

		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}

		return providersubmission.ErrTransientProvider
	}
	defer response.Body.Close()

	statusErr := classifyProviderStatus(response.StatusCode)

	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, maxProviderResponseBytes))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		// if statusCode is 200, but failed to read the response
		if statusErr == nil {
			return providersubmission.ErrTransientProvider
		}
	}

	return statusErr
}

func classifyProviderStatus(statusCode int) error {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return nil
	case statusCode == http.StatusRequestTimeout,
		statusCode == http.StatusTooManyRequests:
		return providersubmission.ErrTransientProvider

	case statusCode >= http.StatusInternalServerError &&
		statusCode < 600:
		return providersubmission.ErrTransientProvider

	default:
		return providersubmission.ErrPermanentProvider
	}
}
