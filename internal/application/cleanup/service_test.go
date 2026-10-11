package cleanup_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

// TestRunMaterializesBeforeSelectingAndMarksOnlyAfterDelete is the application
// tracer for Issue 010 Checkpoint 3.
//
// It intentionally references the user-owned cleanup API before that API exists.
// Keep this as one success behavior. Object-not-found, partial batches, cancellation,
// storage failure, and marker failure remain agent-owned siblings after review.
func TestRunMaterializesBeforeSelectingAndMarksOnlyAfterDelete(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID := uuid.MustParse("17c31962-1864-4cf2-89d0-bb1afcf6e572")
	storageKey := "verification-sessions/5828f559-45cf-4495-af54-983b485504a3/identity_document/17c31962-1864-4cf2-89d0-bb1afcf6e572"
	calls := make([]string, 0, 4)
	repository := &recordingRepository{
		calls: &calls,
		candidates: []cleanup.Candidate{{
			IntentID:   intentID,
			StorageKey: storageKey,
		}},
	}
	storage := &recordingObjectStorage{calls: &calls}
	service := cleanup.NewService(repository, storage, fixedClock{now: now})

	result, err := service.Run(context.Background(), cleanup.Input{Limit: 10})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Materialized != 1 || result.Selected != 1 || result.Deleted != 1 {
		t.Errorf("Run() result = %#v, want materialized:1 selected:1 deleted:1", result)
	}
	wantCalls := []string{
		"materialize@" + now.Format(time.RFC3339),
		"list@" + now.Add(-24*time.Hour).Format(time.RFC3339) + ":10",
		"delete:" + storageKey,
		"mark:" + intentID.String() + "@" + now.Format(time.RFC3339),
	}
	if fmt.Sprint(calls) != fmt.Sprint(wantCalls) {
		t.Errorf("calls = %v, want %v", calls, wantCalls)
	}
}

type recordingRepository struct {
	calls      *[]string
	candidates []cleanup.Candidate
}

func (r *recordingRepository) MaterializeExpiredUploadIntents(
	_ context.Context,
	now time.Time,
) (int64, error) {
	*r.calls = append(*r.calls, "materialize@"+now.Format(time.RFC3339))
	return 1, nil
}

func (r *recordingRepository) ListCleanupCandidates(
	_ context.Context,
	olderThan time.Time,
	limit int32,
) ([]cleanup.Candidate, error) {
	*r.calls = append(
		*r.calls,
		fmt.Sprintf("list@%s:%d", olderThan.Format(time.RFC3339), limit),
	)
	return append([]cleanup.Candidate(nil), r.candidates...), nil
}

func (r *recordingRepository) MarkObjectDeleted(
	_ context.Context,
	intentID uuid.UUID,
	deletedAt time.Time,
) (bool, error) {
	*r.calls = append(
		*r.calls,
		"mark:"+intentID.String()+"@"+deletedAt.Format(time.RFC3339),
	)
	return true, nil
}

type recordingObjectStorage struct {
	calls *[]string
}

func (s *recordingObjectStorage) DeleteObject(_ context.Context, storageKey string) error {
	*s.calls = append(*s.calls, "delete:"+storageKey)
	return nil
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }
