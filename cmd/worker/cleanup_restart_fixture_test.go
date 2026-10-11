package main

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
)

// workerRestartFixture keeps an event history across two simulated process
// lifetimes. It contains no queue or runtime implementation; a later lifecycle
// test can use newProcess to check start, scheduling, drain, and restart order.
type workerRestartFixture struct {
	mu     sync.Mutex
	events []string
}

func (f *workerRestartFixture) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *workerRestartFixture) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *workerRestartFixture) newProcess(name string) (workerRestartServer, workerRestartScheduler, workerRestartRelay) {
	return workerRestartServer{fixture: f, name: name},
		workerRestartScheduler{fixture: f, name: name},
		workerRestartRelay{fixture: f, name: name}
}

type workerRestartServer struct {
	fixture *workerRestartFixture
	name    string
}

func (s workerRestartServer) Start() error {
	s.fixture.record(s.name + ":server:start")
	return nil
}

func (s workerRestartServer) Shutdown() {
	s.fixture.record(s.name + ":server:shutdown")
}

type workerRestartScheduler struct {
	fixture *workerRestartFixture
	name    string
}

func (s workerRestartScheduler) Start() error {
	s.fixture.record(s.name + ":cleanup-scheduler:start")
	return nil
}

func (s workerRestartScheduler) Shutdown() {
	s.fixture.record(s.name + ":cleanup-scheduler:shutdown")
}

type workerRestartRelay struct {
	fixture *workerRestartFixture
	name    string
}

func (r workerRestartRelay) RunOnce(context.Context) error {
	r.fixture.record(r.name + ":relay:run")
	return nil
}

func TestServeWorkerStopsSchedulerBeforeServerAcrossRestarts(t *testing.T) {
	fixture := &workerRestartFixture{}
	for _, name := range []string{"first", "second"} {
		server, scheduler, relay := fixture.newProcess(name)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- serveWorker(ctx, relay, server, time.Hour, scheduler,
				&expiryEnqueuerStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		}()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if len(fixture.snapshot()) >= 3 && fixture.snapshot()[len(fixture.snapshot())-1] == name+":relay:run" {
				break
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s worker exit: %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s worker did not stop", name)
		}
	}
	want := []string{
		"first:server:start", "first:cleanup-scheduler:start", "first:relay:run",
		"first:cleanup-scheduler:shutdown", "first:server:shutdown",
		"second:server:start", "second:cleanup-scheduler:start", "second:relay:run",
		"second:cleanup-scheduler:shutdown", "second:server:shutdown",
	}
	if got := fixture.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("worker lifecycle = %v, want %v", got, want)
	}
}
