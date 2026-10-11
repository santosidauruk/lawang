// Package cleanup owns the application orchestration for selecting and deleting
// safe orphaned Upload Intent objects.
//
// Issue 010 Checkpoint 3 deliberately starts with this package boundary only. The
// first production service, ports, and result contract remain user-owned and are
// driven by service_test.go.
package cleanup

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/session"
)

var ErrCleanupObjectNotFound = errors.New("object not found")
var ErrTransientCleanup = errors.New("transient error")
var ErrPermanentCleanup = errors.New("permanent error")

type Candidate struct {
	IntentID   uuid.UUID
	StorageKey string
}

type Input struct {
	Limit int32
}

type CleanupResult struct {
	Materialized int
	Selected     int
	Deleted      int
}

type CleanupRepository interface {
	MaterializeExpiredUploadIntents(ctx context.Context, deadline time.Time) (int64, error)
	ListCleanupCandidates(ctx context.Context, cutoffTime time.Time, batchSize int32) ([]Candidate, error)
	MarkObjectDeleted(ctx context.Context, uploadIntentId uuid.UUID, deletedAt time.Time) (bool, error)
}

type CleanupStorage interface {
	DeleteObject(ctx context.Context, storageKey string) error
}

type Service struct {
	repository CleanupRepository
	storage    CleanupStorage
	clock      session.Clock
}

func NewService(repository CleanupRepository, storage CleanupStorage, clock session.Clock) *Service {
	return &Service{
		repository: repository,
		storage:    storage,
		clock:      clock,
	}
}

func (s *Service) Run(ctx context.Context, input Input) (CleanupResult, error) {
	var result CleanupResult
	now := s.clock.Now()
	materialized, err := s.repository.MaterializeExpiredUploadIntents(ctx, now)
	if err != nil {
		return CleanupResult{}, err
	}

	candidates, err := s.repository.ListCleanupCandidates(ctx, now.Add(-24*time.Hour), input.Limit)
	if err != nil {
		return CleanupResult{}, err
	}

	var deletionErr error
	for _, candidate := range candidates {
		err := s.storage.DeleteObject(ctx, candidate.StorageKey)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				deletionErr = errors.Join(deletionErr, err)
				break
			}
			if errors.Is(err, ErrCleanupObjectNotFound) {
				markDeleted, markErr := s.repository.MarkObjectDeleted(ctx, candidate.IntentID, now)
				if markErr != nil {
					deletionErr = errors.Join(deletionErr, markErr)
				}
				if markDeleted {
					result.Deleted++
				}
				continue
			}
			if errors.Is(err, ErrTransientCleanup) {
				deletionErr = errors.Join(deletionErr, ErrTransientCleanup)
				continue
			}
			deletionErr = errors.Join(deletionErr, err)
			continue
		}
		markDeleted, markErr := s.repository.MarkObjectDeleted(ctx, candidate.IntentID, now)
		if markErr != nil {
			deletionErr = errors.Join(deletionErr, markErr)
		}
		if markDeleted {
			result.Deleted++
		}
	}

	result.Materialized = int(materialized)
	result.Selected = len(candidates)
	return result, deletionErr

}
