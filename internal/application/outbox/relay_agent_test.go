package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
)

func TestRelayRunOnceTreatsNoAvailableRowAsIdle(t *testing.T) {
	store := &relayStoreStub{claimErr: outbox.ErrNoOutboxAvailable}
	publisher := &relayPublisherStub{enqueueErr: errors.New("publisher must not be called")}
	relay := outbox.NewRelay(store, publisher, relayClock{now: time.Now()}, time.Minute)

	if err := relay.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v, want nil idle result", err)
	}
	if store.claimCalls != 1 || publisher.calls != 0 || store.markCalls != 0 || store.failureCalls != 0 {
		t.Fatalf(
			"idle calls = claim:%d enqueue:%d mark:%d failure:%d, want 1:0:0:0",
			store.claimCalls,
			publisher.calls,
			store.markCalls,
			store.failureCalls,
		)
	}
}

func TestRelayRunOnceRecordsPublishFailureWithoutMarkingPublished(t *testing.T) {
	publishErr := errors.New("queue unavailable")
	claimed := outbox.ClaimedOutbox{
		Task: outbox.Task{
			ID:        uuid.New(),
			Type:      outbox.TaskTypeProviderSubmit,
			SessionID: uuid.New(),
		},
		ClaimToken: uuid.New(),
	}
	store := &relayStoreStub{claimed: claimed}
	publisher := &relayPublisherStub{enqueueErr: publishErr}
	relay := outbox.NewRelay(store, publisher, relayClock{now: time.Now()}, time.Minute)

	err := relay.RunOnce(context.Background())
	if !errors.Is(err, publishErr) {
		t.Fatalf("RunOnce() error = %v, want publish error", err)
	}
	if publisher.calls != 1 || store.failureCalls != 1 || store.markCalls != 0 {
		t.Fatalf(
			"failure calls = enqueue:%d record:%d mark:%d, want 1:1:0",
			publisher.calls,
			store.failureCalls,
			store.markCalls,
		)
	}
	if store.failureOutboxID != claimed.Task.ID || store.failureClaimToken != claimed.ClaimToken {
		t.Fatalf(
			"recorded failure ownership = outbox:%s token:%s, want outbox:%s token:%s",
			store.failureOutboxID,
			store.failureClaimToken,
			claimed.Task.ID,
			claimed.ClaimToken,
		)
	}
}

type relayClock struct {
	now time.Time
}

func (c relayClock) Now() time.Time {
	return c.now
}

type relayPublisherStub struct {
	calls      int
	enqueueErr error
}

func (p *relayPublisherStub) Enqueue(context.Context, outbox.Task) error {
	p.calls++
	return p.enqueueErr
}

type relayStoreStub struct {
	claimed      outbox.ClaimedOutbox
	claimErr     error
	claimCalls   int
	markCalls    int
	failureCalls int

	failureOutboxID   uuid.UUID
	failureClaimToken uuid.UUID
}

func (s *relayStoreStub) ClaimNext(
	context.Context,
	time.Time,
	uuid.UUID,
	time.Time,
) (outbox.ClaimedOutbox, error) {
	s.claimCalls++
	return s.claimed, s.claimErr
}

func (s *relayStoreStub) MarkPublished(context.Context, uuid.UUID, uuid.UUID) error {
	s.markCalls++
	return nil
}

func (s *relayStoreStub) RecordPublishFailure(
	_ context.Context,
	outboxID uuid.UUID,
	claimToken uuid.UUID,
) error {
	s.failureCalls++
	s.failureOutboxID = outboxID
	s.failureClaimToken = claimToken
	return nil
}
