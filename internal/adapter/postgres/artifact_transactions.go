package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	generated "github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

// CHECKPOINT 3 STEP 3 — USER-AUTHORED FIRST ADAPTER OPERATION (COMPLETE)
//
// Reviewed boundary:
//   - the receiver owns transaction-bound generated.Queries;
//   - input is context plus Verification Session ID and Upload Intent ID;
//   - output is artifact.UploadIntent, never a generated sqlc row;
//   - pgx.ErrNoRows maps to artifact.ErrUploadIntentNotFound;
//   - every field is mapped explicitly, including nullable timestamps/failure code.

type ArtifactTransactions struct {
	database transactionBeginner
	queries  *generated.Queries
}

// CHECKPOINT 3 STEP 4 — USER-AUTHORED TRANSACTION TRACER
//
// Complete this step in the following order, stopping at the first compile/test
// failure after each item:
//
//  1. Add the non-transaction generated queries needed by artifact.Reader and write
//     NewArtifactTransactions. The constructor must accept the real pool through the
//     smallest interface that supports both Begin and generated.DBTX.
//  2. Implement Reader.LoadSession, Reader.LoadUploadIntent, and
//     Reader.LoadPersonalDetails. Reuse existing generated session/personal-details
//     queries; do not make duplicate SQL solely to rename a method.
//  3. Implement WithinTransaction using one pgx transaction and one shared
//     generated.Queries value. Defer rollback, return the operation error unchanged,
//     and return the commit error rather than hiding it.
//  4. Construct artifactTransaction with that transaction-bound Queries value and an
//     eventTransaction using the same value.
//  5. Add the remaining artifact.Transaction methods needed by Service.Confirm:
//     LockSession, LoadPersonalDetails, ConfirmUploadIntent,
//     InsertVerificationArtifact, UpdateSessionState, AppendEvent, and
//     MarkUploadIntentValidationFailed.
//  6. For every :execrows query, require exactly one affected row. Map missing reads
//     to the application errors expected by artifact.Service.
//
// External storage and extraction must remain outside WithinTransaction; the
// application service already enforces that ordering.
type artifactTransaction struct {
	queries *generated.Queries
	events  *eventTransaction
}

func NewArtifactTransactions(artifactDB interface {
	transactionBeginner
	generated.DBTX
}) *ArtifactTransactions {
	return &ArtifactTransactions{database: artifactDB, queries: generated.New(artifactDB)}
}

func (t *ArtifactTransactions) WithinTransaction(
	ctx context.Context,
	operation func(artifact.Transaction) error) error {
	tx, err := t.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	queries := generated.New(tx)
	transaction := &artifactTransaction{
		queries: queries,
		events:  &eventTransaction{queries: queries},
	}
	if err := operation(transaction); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (t *artifactTransaction) LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
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

	return session.VerificationSession{
		ID: row.ID, Status: state, ResumeTokenHash: row.ResumeTokenHash,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (t *artifactTransaction) LockUploadIntent(ctx context.Context, sessionID uuid.UUID, uploadIntentID uuid.UUID) (artifact.UploadIntent, error) {
	row, err := t.queries.LockUploadIntent(ctx, generated.LockUploadIntentParams{
		VerificationSessionID: sessionID,
		UploadIntentID:        uploadIntentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}

	if err != nil {
		return artifact.UploadIntent{}, err
	}

	confirmedAt, err := timestamptzToTimePtr(row.ConfirmedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}

	objectDeletedAt, err := timestamptzToTimePtr(row.ObjectDeletedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}

	failureCode := pgxTextToStringPtr(row.FailureCode)

	return artifact.UploadIntent{
		ID:                    row.ID,
		VerificationSessionID: row.VerificationSessionID,
		Kind:                  row.Kind,
		StorageKey:            row.StorageKey,
		Status:                row.Status,
		CreatedAt:             row.CreatedAt,
		LatestStatusChangeAt:  row.LatestStatusChangeAt,
		ExpiresAt:             row.ExpiresAt,
		ConfirmedAt:           confirmedAt,
		ObjectDeletedAt:       objectDeletedAt,
		FailureCode:           failureCode,
	}, nil
}

func (t *artifactTransaction) LoadPersonalDetails(ctx context.Context, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	detailsRow, err := t.queries.GetPersonalDetailsBySessionID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return personaldetails.PersonalDetails{}, artifact.ErrPersonalDetailsNotFound
	}
	if err != nil {
		return personaldetails.PersonalDetails{}, err
	}

	return personaldetails.PersonalDetails{
		SessionID: detailsRow.VerificationSessionID,
		Input: personaldetails.Input{
			FullName: detailsRow.FullName, DateOfBirth: detailsRow.DateOfBirth,
			IdentityNumber: detailsRow.IdentityNumber, Address: detailsRow.Address,
		},
		CreatedAt: detailsRow.CreatedAt,
	}, nil
}

func (t *artifactTransaction) ConfirmUploadIntent(ctx context.Context, uploadIntentID uuid.UUID, confirmedAt time.Time) error {
	rowsAffected, err := t.queries.ConfirmUploadIntent(ctx, generated.ConfirmUploadIntentParams{
		UploadIntentID:                   uploadIntentID,
		UploadIntentLatestStatusChangeAt: confirmedAt,
		UploadIntentConfirmedAt: pgtype.Timestamptz{
			Time:             confirmedAt,
			InfinityModifier: pgtype.Finite,
			Valid:            true,
		},
	})
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return errors.New("confirm upload intent error")
	}

	return nil
}

func (t *artifactTransaction) InsertVerificationArtifact(ctx context.Context, insertedArtifact artifact.VerificationArtifact) error {
	err := t.queries.InsertVerificationArtifact(ctx, generated.InsertVerificationArtifactParams{
		VerificationArtifactID:          insertedArtifact.ID,
		VerificationSessionID:           insertedArtifact.VerificationSessionID,
		UploadIntentID:                  insertedArtifact.UploadIntentID,
		VerificationArtifactKind:        insertedArtifact.Kind,
		VerificationArtifactStorageKey:  insertedArtifact.StorageKey,
		VerificationArtifactContentType: insertedArtifact.ContentType,
		VerificationArtifactSizeBytes:   insertedArtifact.SizeBytes,
		VerificationArtifactEtag:        insertedArtifact.ETag,
		VerificationArtifactCreatedAt:   insertedArtifact.CreatedAt,
	})
	if err != nil {
		return err
	}

	return nil
}

func (t *artifactTransaction) UpdateSessionState(ctx context.Context, sessionID uuid.UUID, expected verificationsession.State, next verificationsession.State, updatedAt time.Time) error {
	return t.events.UpdateState(ctx, sessionID, expected, next, updatedAt)
}

func (t *artifactTransaction) MarkUploadIntentValidationFailed(ctx context.Context, uploadIntentID uuid.UUID, failureCode string, updatedAt time.Time) error {
	rowsAffected, err := t.queries.MarkUploadIntentValidationFailed(ctx, generated.MarkUploadIntentValidationFailedParams{
		UploadIntentID: uploadIntentID,
		UploadIntentFailureCode: pgtype.Text{
			String: failureCode,
			Valid:  true,
		},
		UploadIntentLatestStatusChangeAt: updatedAt,
	})
	if err != nil {
		return err
	}

	if rowsAffected != 1 {
		return errors.New("mark upload intent validation failed error")
	}

	return nil
}

func (t *artifactTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return t.events.AppendEvent(ctx, event)
}

func timestamptzToTimePtr(value pgtype.Timestamptz) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}

	if value.InfinityModifier != pgtype.Finite {
		return nil, errors.New("infinite timestamp is unsupported")
	}

	converted := value.Time
	return &converted, nil
}

func pgxTextToStringPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}

	return &value.String
}

func (t *ArtifactTransactions) LoadSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
	row, err := t.queries.GetVerificationSessionByID(ctx, sessionID)
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

func (t *ArtifactTransactions) LoadUploadIntent(ctx context.Context, sessionID uuid.UUID, uploadIntentID uuid.UUID) (artifact.UploadIntent, error) {
	row, err := t.queries.LoadUploadIntent(ctx, generated.LoadUploadIntentParams{
		VerificationSessionID: sessionID,
		UploadIntentID:        uploadIntentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}
	if err != nil {
		return artifact.UploadIntent{}, err
	}

	confirmedAt, err := timestamptzToTimePtr(row.ConfirmedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}

	objectDeletedAt, err := timestamptzToTimePtr(row.ObjectDeletedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}

	failureCode := pgxTextToStringPtr(row.FailureCode)

	return artifact.UploadIntent{
		ID:                    row.ID,
		VerificationSessionID: row.VerificationSessionID,
		Kind:                  row.Kind,
		StorageKey:            row.StorageKey,
		Status:                row.Status,
		CreatedAt:             row.CreatedAt,
		LatestStatusChangeAt:  row.LatestStatusChangeAt,
		ExpiresAt:             row.ExpiresAt,
		ConfirmedAt:           confirmedAt,
		ObjectDeletedAt:       objectDeletedAt,
		FailureCode:           failureCode,
	}, nil
}

func (t *ArtifactTransactions) LoadPersonalDetails(ctx context.Context, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	detailsRow, err := t.queries.GetPersonalDetailsBySessionID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return personaldetails.PersonalDetails{}, artifact.ErrPersonalDetailsNotFound
	}
	if err != nil {
		return personaldetails.PersonalDetails{}, err
	}
	return personaldetails.PersonalDetails{
		SessionID: detailsRow.VerificationSessionID,
		Input: personaldetails.Input{
			FullName: detailsRow.FullName, DateOfBirth: detailsRow.DateOfBirth,
			IdentityNumber: detailsRow.IdentityNumber, Address: detailsRow.Address,
		},
		CreatedAt: detailsRow.CreatedAt,
	}, nil
}
