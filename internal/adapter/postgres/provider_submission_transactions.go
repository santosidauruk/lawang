package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	generated "github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

type ProviderSubmissionTransactions struct {
	database transactionBeginner
	queries  *sqlc.Queries
}

type providerTransaction struct {
	queries *sqlc.Queries
	events  *eventTransaction
}

func NewProviderSubmissionTransactions(db interface {
	transactionBeginner
	sqlc.DBTX
}) *ProviderSubmissionTransactions {
	return &ProviderSubmissionTransactions{
		database: db,
		queries:  sqlc.New(db),
	}
}

func (t *ProviderSubmissionTransactions) WithinTransaction(
	ctx context.Context,
	operation func(providersubmission.Transaction) error) error {
	tx, err := t.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	queries := generated.New(tx)
	transaction := &providerTransaction{
		queries: queries,
		events:  &eventTransaction{queries: queries},
	}
	if err := operation(transaction); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (t *providerTransaction) LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
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

func (t *providerTransaction) HasRequiredAcceptedArtifacts(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	b, err := t.queries.HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if !b.Valid {
		return false, errors.New("artifacts is not accepted yet")
	}
	return b.Bool, nil
}

func (t *providerTransaction) SetPendingVerification(ctx context.Context, submittedAt time.Time, sessionID uuid.UUID) error {
	rowsAffected, err := t.queries.SetPendingVerification(ctx, sqlc.SetPendingVerificationParams{
		SubmittedAt: submittedAt,
		SessionID:   sessionID,
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errors.New("failed to set verification to pending")
	}
	return nil
}

func (t *providerTransaction) InsertUnpublishedOutbox(ctx context.Context, outboxID uuid.UUID, sessionID uuid.UUID) error {
	return t.queries.InsertUnpublishedOutbox(ctx, sqlc.InsertUnpublishedOutboxParams{
		ID:                    outboxID,
		VerificationSessionID: sessionID,
	})
}

func (t *providerTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return t.events.AppendEvent(ctx, event)
}
