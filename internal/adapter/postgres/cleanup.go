package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

type CleanupStore struct {
	queries *sqlc.Queries
}

func NewCleanupRepository(artifactDB interface {
	sqlc.DBTX
}) *CleanupStore {
	return &CleanupStore{
		queries: sqlc.New(artifactDB),
	}
}

func (c *CleanupStore) MaterializeExpiredUploadIntents(ctx context.Context, deadline time.Time) (int64, error) {
	return c.queries.MaterializeExpiredUploadIntents(ctx, deadline)
}

func (c *CleanupStore) ListCleanupCandidates(ctx context.Context, cutoffTime time.Time, batchSize int32) ([]cleanup.Candidate, error) {
	rows, err := c.queries.SelectStaleUploadIntents(ctx, sqlc.SelectStaleUploadIntentsParams{
		CutoffTime: cutoffTime,
		BatchSize:  batchSize,
	})

	if err != nil {
		return []cleanup.Candidate{}, err
	}

	var result []cleanup.Candidate
	for _, v := range rows {
		item := cleanup.Candidate{
			IntentID:   v.ID,
			StorageKey: v.StorageKey,
		}
		result = append(result, item)
	}

	return result, nil
}

func (c *CleanupStore) MarkObjectDeleted(ctx context.Context, uploadIntentId uuid.UUID, deletedAt time.Time) (bool, error) {
	affectedRows, err := c.queries.MarkDeletedIntentById(ctx, sqlc.MarkDeletedIntentByIdParams{
		DeletedAt:      deletedAt,
		UploadIntentID: uploadIntentId,
	})
	if err != nil {
		return false, err
	}
	if affectedRows == 0 {
		return false, nil
	}
	return true, nil
}
