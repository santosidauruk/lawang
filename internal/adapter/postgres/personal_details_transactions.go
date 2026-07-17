package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	generated "github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

type PersonalDetailsTransactions struct {
	database transactionBeginner
}

func NewPersonalDetailsTransactions(database interface {
	transactionBeginner
	generated.DBTX
}) *PersonalDetailsTransactions {
	return &PersonalDetailsTransactions{database: database}
}

func (t *PersonalDetailsTransactions) WithinTransaction(
	ctx context.Context,
	operation func(personaldetails.Transaction) error,
) error {
	tx, err := t.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := generated.New(tx)
	transaction := &personalDetailsTransaction{
		queries: queries,
		events:  &eventTransaction{queries: queries},
	}
	if err := operation(transaction); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type personalDetailsTransaction struct {
	queries *generated.Queries
	events  *eventTransaction
}

func (t *personalDetailsTransaction) LockSession(
	ctx context.Context,
	id uuid.UUID,
) (session.VerificationSession, error) {
	row, err := t.queries.LockVerificationSessionForPersonalDetails(ctx, id)
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
	return session.VerificationSession{
		ID: row.ID, Status: state, ResumeTokenHash: row.ResumeTokenHash,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (t *personalDetailsTransaction) LoadDetails(
	ctx context.Context,
	id uuid.UUID,
) (*personaldetails.PersonalDetails, error) {
	detailsRow, err := t.queries.GetPersonalDetailsBySessionID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &personaldetails.PersonalDetails{
		SessionID: detailsRow.VerificationSessionID,
		Input: personaldetails.Input{
			FullName: detailsRow.FullName, DateOfBirth: detailsRow.DateOfBirth,
			IdentityNumber: detailsRow.IdentityNumber, Address: detailsRow.Address,
		},
		CreatedAt: detailsRow.CreatedAt,
	}, nil
}

func (t *personalDetailsTransaction) InsertDetails(
	ctx context.Context,
	details personaldetails.PersonalDetails,
) error {
	return t.queries.InsertPersonalDetails(ctx, generated.InsertPersonalDetailsParams{
		VerificationSessionID: details.SessionID,
		FullName:              details.FullName,
		DateOfBirth:           details.DateOfBirth,
		IdentityNumber:        details.IdentityNumber,
		Address:               details.Address,
		CreatedAt:             details.CreatedAt,
	})
}

func (t *personalDetailsTransaction) UpdateState(
	ctx context.Context,
	id uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	updatedAt time.Time,
) error {
	return t.events.UpdateState(ctx, id, expected, next, updatedAt)
}

func (t *personalDetailsTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return t.events.AppendEvent(ctx, event)
}
