package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
)

type OutboxStore struct {
	database transactionBeginner
	queries  *sqlc.Queries
}

func NewOutboxStore(database interface {
	transactionBeginner
	sqlc.DBTX
}) *OutboxStore {
	return &OutboxStore{
		database: database,
		queries:  sqlc.New(database),
	}
}

func (o *OutboxStore) ClaimNext(ctx context.Context, claimedAt time.Time, claimToken uuid.UUID, claimedUntil time.Time) (outbox.ClaimedOutbox, error) {
	tx, err := o.database.Begin(ctx)
	if err != nil {
		return outbox.ClaimedOutbox{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	queries := sqlc.New(tx)

	row, err := queries.ClaimNextOutbox(ctx, claimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return outbox.ClaimedOutbox{}, outbox.ErrNoOutboxAvailable
	}
	if err != nil {
		return outbox.ClaimedOutbox{}, err
	}

	rowsAffected, err := queries.FillClaimTokenOutbox(ctx, sqlc.FillClaimTokenOutboxParams{
		ClaimToken:   pgtype.UUID{Bytes: claimToken, Valid: true},
		ClaimedUntil: claimedUntil,
		OutboxID:     row.ID,
	})
	if err != nil {
		return outbox.ClaimedOutbox{}, err
	}
	if rowsAffected != 1 {
		return outbox.ClaimedOutbox{}, fmt.Errorf("claim outbox row: %w", outbox.ErrClaimOwnershipLost)
	}

	if err = tx.Commit(ctx); err != nil {
		return outbox.ClaimedOutbox{}, err
	}

	return outbox.ClaimedOutbox{
		Task: outbox.Task{
			ID:        row.ID,
			Type:      row.TaskType,
			SessionID: row.VerificationSessionID,
		},
		ClaimToken: claimToken,
	}, nil
}

func (o *OutboxStore) MarkPublished(ctx context.Context, outboxID uuid.UUID, claimToken uuid.UUID) error {
	rowsAffected, err := o.queries.MarkPublishedOutbox(ctx, sqlc.MarkPublishedOutboxParams{
		OutboxID:   outboxID,
		ClaimToken: pgtype.UUID{Bytes: claimToken, Valid: true},
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("mark outbox published: %w", outbox.ErrClaimOwnershipLost)
	}
	return nil
}

func (o *OutboxStore) RecordPublishFailure(ctx context.Context, outboxID uuid.UUID, claimToken uuid.UUID) error {
	rowsAffected, err := o.queries.RecordOutboxPublishFailure(ctx, sqlc.RecordOutboxPublishFailureParams{
		OutboxID:   outboxID,
		ClaimToken: pgtype.UUID{Bytes: claimToken, Valid: true},
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("record outbox publish failure: %w", outbox.ErrClaimOwnershipLost)
	}
	return nil
}
