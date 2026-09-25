package expiry_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

// TestSweepUsesOneBoundedDueBatchAndTheGuardedExpiryOperation is the scheduled
// half of the Checkpoint 2 convergence tracer. The reader only selects IDs; the
// sweep must release that read before it invokes the exact Checkpoint 1 operation
// once per ID. Failure accounting and cancellation remain sibling coverage after
// the first focused GREEN and review gate.
func TestSweepUsesOneBoundedDueBatchAndTheGuardedExpiryOperation(t *testing.T) {
	now := time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)
	firstID := uuid.MustParse("70979490-2aa9-4d68-a675-27c3ab05f2df")
	secondID := uuid.MustParse("e9051ddb-9046-4f71-bc3e-d610b96b8b63")
	reader := &recordingDueSessionReader{ids: []uuid.UUID{firstID, secondID}}
	operation := &recordingExpiryOperation{
		results: map[uuid.UUID]expiry.Result{
			firstID:  {ID: firstID, Status: verificationsession.Expired, Changed: true},
			secondID: {ID: secondID, Status: verificationsession.Expired, Changed: false},
		},
		readerActive: &reader.active,
	}

	service := expiry.NewSweepService(reader, operation, fixedClock{now: now}, 2)
	got, err := service.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if !reader.at.Equal(now) || reader.limit != 2 {
		t.Errorf("due reader args = at:%s limit:%d, want %s/2", reader.at, reader.limit, now)
	}
	inputs := operation.recordedInputs()
	seen := make(map[uuid.UUID]int, len(inputs))
	for _, input := range inputs {
		seen[input.SessionID]++
	}
	if len(inputs) != 2 || seen[firstID] != 1 || seen[secondID] != 1 {
		t.Errorf("expiry inputs = %#v, want IDs %s and %s exactly once", inputs, firstID, secondID)
	}
	if got.Processed != 2 || got.Expired != 1 || got.NoOp != 1 || got.Failed != 0 {
		t.Errorf("Sweep() result = %#v, want processed:2 expired:1 no-op:1 failed:0", got)
	}
}

type recordingDueSessionReader struct {
	ids    []uuid.UUID
	at     time.Time
	limit  int
	active bool
}

func (r *recordingDueSessionReader) FindDueSessionIDs(
	_ context.Context,
	at time.Time,
	limit int,
) ([]uuid.UUID, error) {
	r.active = true
	defer func() { r.active = false }()
	r.at = at
	r.limit = limit
	return append([]uuid.UUID(nil), r.ids...), nil
}

type recordingExpiryOperation struct {
	mu           sync.Mutex
	inputs       []expiry.Input
	results      map[uuid.UUID]expiry.Result
	readerActive *bool
}

func (o *recordingExpiryOperation) Expire(
	_ context.Context,
	input expiry.Input,
) (expiry.Result, error) {
	if *o.readerActive {
		panic("expiry operation called while due-session reader is still active")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inputs = append(o.inputs, input)
	return o.results[input.SessionID], nil
}

func (o *recordingExpiryOperation) recordedInputs() []expiry.Input {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]expiry.Input(nil), o.inputs...)
}
