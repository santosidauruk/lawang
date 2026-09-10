package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/santosidauruk/lawang/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang/internal/application/providerverdict"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/verdict"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

type ProviderVerdictTransactions struct {
	database transactionBeginner
	queries  *sqlc.Queries
}

type providerVerdictTransaction struct {
	queries *sqlc.Queries
	events  *eventTransaction
}

func NewProviderVerdictTransactions(database interface {
	transactionBeginner
	sqlc.DBTX
}) *ProviderVerdictTransactions {
	return &ProviderVerdictTransactions{
		database: database,
		queries:  sqlc.New(database),
	}
}

func (p *ProviderVerdictTransactions) WithinTransaction(
	ctx context.Context,
	operation func(providerverdict.Transaction) error) error {
	tx, err := p.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	queries := sqlc.New(tx)
	transaction := &providerVerdictTransaction{
		queries: queries,
		events:  &eventTransaction{queries: queries},
	}
	if err := operation(transaction); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (p *providerVerdictTransaction) InsertWebhookEvent(ctx context.Context, eventID uuid.UUID, sessionID uuid.UUID, payload []byte, receivedAt time.Time) (providerverdict.InsertWebhookEventOutcome, error) {
	insertedID, err := p.queries.InsertWebhookEvent(ctx, sqlc.InsertWebhookEventParams{
		WebhookEventID: eventID,
		SessionID:      sessionID,
		Payload:        payload,
		ReceivedAt:     receivedAt,
	})

	if errors.Is(err, pgx.ErrNoRows) {
		return providerverdict.WebhookEventDuplicate, nil
	}
	if err != nil {
		return 0, err
	}
	if insertedID != eventID {
		return 0, errors.New("inserted event ID mismatch")
	}
	return providerverdict.WebhookEventInserted, nil
}

func (p *providerVerdictTransaction) LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
	row, err := p.queries.LockVerificationSessionByID(ctx, sessionID)
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

func (p *providerVerdictTransaction) VerifySession(ctx context.Context, verifiedAt time.Time, sessionID uuid.UUID) error {
	rowsAffected, err := p.queries.UpdateSessionToVerified(ctx, sqlc.UpdateSessionToVerifiedParams{
		VerifiedAt: pgtype.Timestamptz{Time: verifiedAt, Valid: true},
		SessionID:  sessionID,
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errors.New("failed to verify session")
	}
	return nil
}

func (p *providerVerdictTransaction) RejectSession(ctx context.Context, rejectedAt time.Time, sessionID uuid.UUID, rejectionReason verdict.RejectionReason) error {
	rowsAffected, err := p.queries.UpdateSessionToRejected(ctx, sqlc.UpdateSessionToRejectedParams{
		RejectedAt:      pgtype.Timestamptz{Time: rejectedAt, Valid: true},
		RejectionReason: pgtype.Text{String: rejectionReason.String(), Valid: true},
		SessionID:       sessionID,
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errors.New("session is not pending")
	}
	return nil
}

func (p *providerVerdictTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return p.events.AppendEvent(ctx, event)
}

func (p *providerVerdictTransaction) markEvent(ctx context.Context, webhookID uuid.UUID, processedAt time.Time, processingStatus string, ignoreReason pgtype.Text) error {
	rowsAffected, err := p.queries.MarkWebhookEvents(ctx, sqlc.MarkWebhookEventsParams{
		ProcessingStatus: pgtype.Text{String: processingStatus, Valid: true},
		ProcessedAt:      pgtype.Timestamptz{Time: processedAt, Valid: true},
		IgnoreReason:     ignoreReason,
		WebhookEventID:   webhookID,
	})
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return errors.New("webhook already processed")
	}
	return nil
}

func (p *providerVerdictTransaction) MarkEventApplied(ctx context.Context, webhookID uuid.UUID, processedAt time.Time) error {
	return p.markEvent(ctx, webhookID, processedAt, "applied", pgtype.Text{})
}

func (p *providerVerdictTransaction) MarkEventIgnored(ctx context.Context, webhookID uuid.UUID, processedAt time.Time, reason providerverdict.IgnoreReason) error {
	return p.markEvent(ctx, webhookID, processedAt, "ignored", pgtype.Text{String: string(reason), Valid: true})
}
