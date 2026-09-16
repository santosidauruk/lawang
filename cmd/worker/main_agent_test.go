package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type relayRunnerStub struct {
	run chan struct{}
}

func (r relayRunnerStub) RunOnce(context.Context) error {
	select {
	case r.run <- struct{}{}:
	default:
	}
	return nil
}

type taskServerStub struct {
	startErr error
	mu       sync.Mutex
	started  bool
	stopped  bool
}

func (s *taskServerStub) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = true
	return s.startErr
}

func (s *taskServerStub) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

func TestServeWorkerRunsRelayAndStopsServerOnCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	relayRan := make(chan struct{}, 1)
	server := &taskServerStub{}
	done := make(chan error, 1)
	go func() {
		done <- serveWorker(
			ctx,
			relayRunnerStub{run: relayRan},
			server,
			time.Hour,
			slog.New(slog.NewTextHandler(io.Discard, nil)),
		)
	}()

	select {
	case <-relayRan:
	case <-time.After(time.Second):
		t.Fatal("worker did not run relay immediately")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve worker after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.started || !server.stopped {
		t.Fatalf("expected server start and shutdown, got started=%t stopped=%t", server.started, server.stopped)
	}
}

func TestServeWorkerReturnsServerStartFailureWithoutShutdown(t *testing.T) {
	t.Parallel()

	startErr := errors.New("redis unavailable")
	server := &taskServerStub{startErr: startErr}
	err := serveWorker(
		context.Background(),
		relayRunnerStub{run: make(chan struct{}, 1)},
		server,
		time.Hour,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if !errors.Is(err, startErr) {
		t.Fatalf("expected start failure, got %v", err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.stopped {
		t.Fatal("server that failed to start must not be shut down")
	}
}
