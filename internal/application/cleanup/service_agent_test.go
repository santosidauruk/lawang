package cleanup_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

func TestRunWithEmptyBatchDoesNotDeleteOrMark(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{materialized: 2}
	storage := &cleanupStorageStub{}
	service := cleanup.NewService(repository, storage, fixedClock{now: now})

	result, err := service.Run(context.Background(), cleanup.Input{Limit: 10})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Materialized != 2 || result.Selected != 0 || result.Deleted != 0 {
		t.Errorf("Run() result = %#v, want materialized:2 selected:0 deleted:0", result)
	}
	if len(storage.deletedKeys) != 0 {
		t.Errorf("DeleteObject calls = %v, want none", storage.deletedKeys)
	}
	if len(repository.markedIDs) != 0 {
		t.Errorf("MarkObjectDeleted calls = %v, want none", repository.markedIDs)
	}
}

func TestRunWithPartialBatchMarksOnlySuccessfullyDeletedObjects(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	deletedID := uuid.MustParse("1c005ed3-993b-4f1b-ab25-a2c97d1e4ee4")
	retryID := uuid.MustParse("b2972fc7-cc0c-4326-bc92-c744a58b1133")
	repository := &cleanupRepositoryStub{
		candidates: []cleanup.Candidate{
			{IntentID: deletedID, StorageKey: "safe/deleted"},
			{IntentID: retryID, StorageKey: "safe/retry"},
		},
		markResult: true,
	}
	storage := &cleanupStorageStub{errors: map[string]error{
		"safe/retry": cleanup.ErrTransientCleanup,
	}}
	service := cleanup.NewService(repository, storage, fixedClock{now: now})

	result, err := service.Run(context.Background(), cleanup.Input{Limit: 10})
	if !errors.Is(err, cleanup.ErrTransientCleanup) {
		t.Fatalf("Run() error = %v, want ErrTransientCleanup", err)
	}
	if result.Selected != 2 || result.Deleted != 1 {
		t.Errorf("Run() result = %#v, want selected:2 deleted:1", result)
	}
	if len(repository.markedIDs) != 1 || repository.markedIDs[0] != deletedID {
		t.Errorf("marked IDs = %v, want only %s", repository.markedIDs, deletedID)
	}
}

func TestRunStopsBatchWhenObjectDeletionIsCanceled(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	repository := &cleanupRepositoryStub{
		candidates: []cleanup.Candidate{
			{IntentID: uuid.MustParse("16bc0fcf-7858-47bf-9b75-b91495114f94"), StorageKey: "safe/canceled"},
			{IntentID: uuid.MustParse("a62beb2b-e297-44e3-95bf-f98429f86f85"), StorageKey: "safe/must-not-run"},
		},
		markResult: true,
	}
	storage := &cleanupStorageStub{errors: map[string]error{
		"safe/canceled": context.Canceled,
	}}
	service := cleanup.NewService(repository, storage, fixedClock{now: now})

	result, err := service.Run(context.Background(), cleanup.Input{Limit: 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if result.Selected != 2 || result.Deleted != 0 {
		t.Errorf("Run() result = %#v, want selected:2 deleted:0", result)
	}
	if len(storage.deletedKeys) != 1 || storage.deletedKeys[0] != "safe/canceled" {
		t.Errorf("DeleteObject calls = %v, want only canceled candidate", storage.deletedKeys)
	}
	if len(repository.markedIDs) != 0 {
		t.Errorf("marked IDs = %v, want none", repository.markedIDs)
	}
}

func TestRunReturnsMarkerFailureAfterPhysicalDeletion(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	intentID := uuid.MustParse("952983ab-3328-410f-88b6-b1cd95e13a44")
	markerErr := errors.New("persist deletion marker")
	repository := &cleanupRepositoryStub{
		candidates: []cleanup.Candidate{{IntentID: intentID, StorageKey: "safe/marker-failure"}},
		markResult: false,
		markError:  markerErr,
	}
	storage := &cleanupStorageStub{}
	service := cleanup.NewService(repository, storage, fixedClock{now: now})

	result, err := service.Run(context.Background(), cleanup.Input{Limit: 10})
	if !errors.Is(err, markerErr) {
		t.Fatalf("Run() error = %v, want marker failure", err)
	}
	if result.Selected != 1 || result.Deleted != 0 {
		t.Errorf("Run() result = %#v, want selected:1 deleted:0 without durable marker", result)
	}
	if len(storage.deletedKeys) != 1 || storage.deletedKeys[0] != "safe/marker-failure" {
		t.Errorf("DeleteObject calls = %v, want object deletion before marker", storage.deletedKeys)
	}
	if len(repository.markedIDs) != 1 || repository.markedIDs[0] != intentID {
		t.Errorf("marked IDs = %v, want %s", repository.markedIDs, intentID)
	}
}

type cleanupRepositoryStub struct {
	materialized int64
	candidates   []cleanup.Candidate
	markedIDs    []uuid.UUID
	markError    error
	markResult   bool
}

func (r *cleanupRepositoryStub) MaterializeExpiredUploadIntents(
	context.Context,
	time.Time,
) (int64, error) {
	return r.materialized, nil
}

func (r *cleanupRepositoryStub) ListCleanupCandidates(
	context.Context,
	time.Time,
	int32,
) ([]cleanup.Candidate, error) {
	return append([]cleanup.Candidate(nil), r.candidates...), nil
}

func (r *cleanupRepositoryStub) MarkObjectDeleted(
	_ context.Context,
	intentID uuid.UUID,
	_ time.Time,
) (bool, error) {
	r.markedIDs = append(r.markedIDs, intentID)
	return r.markResult, r.markError
}

type cleanupStorageStub struct {
	deletedKeys []string
	errors      map[string]error
}

func (s *cleanupStorageStub) DeleteObject(_ context.Context, storageKey string) error {
	s.deletedKeys = append(s.deletedKeys, storageKey)
	return s.errors[storageKey]
}
