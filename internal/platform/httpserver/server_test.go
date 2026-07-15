package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/santosidauruk/lawang-go/internal/platform/httpserver"
)

type notifyingListener struct {
	accepting chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (listener *notifyingListener) Accept() (net.Conn, error) {
	select {
	case listener.accepting <- struct{}{}:
	default:
	}
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *notifyingListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (listener *notifyingListener) Addr() net.Addr {
	return testAddress("test-listener")
}

type testAddress string

func (address testAddress) Network() string { return "test" }
func (address testAddress) String() string  { return string(address) }

func TestRunShutsDownWhenContextIsCancelled(t *testing.T) {
	listener := &notifyingListener{
		accepting: make(chan struct{}, 1),
		closed:    make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- httpserver.Run(ctx, &http.Server{Handler: http.NotFoundHandler()}, listener, time.Second)
	}()

	select {
	case <-listener.accepting:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("server did not begin accepting connections")
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down after cancellation")
	}
}

func TestNewConfiguresServerTimeouts(t *testing.T) {
	server := httpserver.New("127.0.0.1:9090", http.NotFoundHandler())

	if server.Addr != "127.0.0.1:9090" {
		t.Errorf("Addr = %q, want %q", server.Addr, "127.0.0.1:9090")
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf(
			"timeouts must be positive: readHeader=%s read=%s write=%s idle=%s",
			server.ReadHeaderTimeout,
			server.ReadTimeout,
			server.WriteTimeout,
			server.IdleTimeout,
		)
	}
}
