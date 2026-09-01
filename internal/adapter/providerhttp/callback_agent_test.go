package providerhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/providerhttp"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
)

func TestCallbackSenderRejectsNonSuccessWithoutExposingResponse(t *testing.T) {
	const sensitiveBody = "applicant identity was rejected: 3173000000000999"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		_, _ = response.Write([]byte(sensitiveBody))
	}))
	t.Cleanup(server.Close)
	sender := providerhttp.NewCallbackSender("callback-agent-secret")

	err := sender.Send(context.Background(), server.URL+"/sensitive-callback", fakeprovider.WebhookEvent{
		EventID:   uuid.MustParse("67aa03a7-598d-47a7-96e9-bb575183eb12"),
		SessionID: uuid.MustParse("3184c945-7ac4-4ced-a14e-62834a430261"),
		Verdict:   fakeprovider.Verified,
	})

	if !errors.Is(err, providerhttp.ErrCallbackNonSuccess) {
		t.Fatalf("Send() error = %v, want ErrCallbackNonSuccess", err)
	}
	if strings.Contains(err.Error(), sensitiveBody) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("Send() error exposes callback data: %v", err)
	}
}

func TestCallbackSenderBoundsTimeoutWithoutExposingURL(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		requestStarted <- struct{}{}
		<-releaseResponse
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(releaseResponse) })
	sender := providerhttp.NewCallbackSender("callback-timeout-secret")
	ctx := &controlledDeadlineContext{done: make(chan struct{})}
	results := make(chan error, 1)

	go func() {
		results <- sender.Send(ctx, server.URL+"/private-callback?token=secret", fakeprovider.WebhookEvent{
			EventID:   uuid.MustParse("844f2ebd-91d3-40c9-996f-911137aebeb9"),
			SessionID: uuid.MustParse("c596c44a-84c7-42b2-a741-3b3b3f8919bc"),
			Verdict:   fakeprovider.Verified,
		})
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("callback request did not reach server before timeout")
	}
	close(ctx.done)
	var err error
	select {
	case err = <-results:
	case <-time.After(time.Second):
		t.Fatal("callback request did not stop after deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send() error = %v, want context deadline exceeded", err)
	}
	if strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("Send() timeout exposes callback URL: %v", err)
	}
}

type controlledDeadlineContext struct {
	done chan struct{}
}

func (*controlledDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *controlledDeadlineContext) Done() <-chan struct{}     { return c.done }
func (c *controlledDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
func (*controlledDeadlineContext) Value(any) any { return nil }
