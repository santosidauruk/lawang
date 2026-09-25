package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santosidauruk/lawang/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang/internal/application/expiry"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

type ExpiryTransactions struct {
	database transactionBeginner
	queries  *sqlc.Queries
}

func NewExpiryTransactions(pool interface {
	transactionBeginner
	sqlc.DBTX
}) *ExpiryTransactions {
	return &ExpiryTransactions{
		database: pool,
		queries:  sqlc.New(pool),
	}
}

func (t *ExpiryTransactions) WithinTransaction(
	ctx context.Context,
	operation func(expiry.Transaction) error) error {
	tx, err := t.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	queries := sqlc.New(tx)
	transaction := &expiryTransaction{
		queries: queries,
		events:  &eventTransaction{queries: queries},
	}
	if err := operation(transaction); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type expiryTransaction struct {
	queries *sqlc.Queries
	events  *eventTransaction
}

func (t *expiryTransaction) LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
	row, err := t.queries.LockVerificationSessionByID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	if err != nil {
		return session.VerificationSession{}, err
	}

	state, err := verificationsession.ParseState(row.Status)
	if err != nil {
		return session.VerificationSession{}, err
	}

	var verificationDeadlineAt *time.Time
	if row.VerificationDeadlineAt.Valid {
		verificationDeadlineAt = &row.VerificationDeadlineAt.Time
	}

	return session.VerificationSession{
		ID: row.ID, Status: state, ResumeTokenHash: row.ResumeTokenHash,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, VerificationDeadlineAt: verificationDeadlineAt,
	}, nil
}

func (t *expiryTransaction) GuardExpiry(ctx context.Context, sessionID uuid.UUID, currentState verificationsession.State, expiredAt time.Time) (bool, error) {
	rowsAffected, err := t.queries.ExpireSessionByID(ctx, sqlc.ExpireSessionByIDParams{
		ID:           sessionID,
		UpdatedAt:    expiredAt,
		CurrentState: currentState.String(),
	})
	if err != nil {
		return false, err
	}
	if rowsAffected == 0 {
		return false, nil
	}
	if rowsAffected > 1 {
		return false, expiry.ErrMultipleExpireSession
	}

	return true, nil
}

func (t *expiryTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return t.events.AppendEvent(ctx, event)
}

func (t *ExpiryTransactions) FindDueSessionIDs(ctx context.Context, deadline time.Time, batchSize int) ([]uuid.UUID, error) {
	return t.queries.GetDueSessionIDs(ctx, sqlc.GetDueSessionIDsParams{
		Deadline: pgtype.Timestamptz{
			Time:  deadline,
			Valid: true,
		},
		BatchSize: int32(batchSize),
	})
}
