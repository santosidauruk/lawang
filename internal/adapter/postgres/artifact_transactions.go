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

type ArtifactTransactions struct {
	database transactionBeginner
	queries  *generated.Queries
}

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

func (t *ArtifactTransactions) WithinUploadIntentTransaction(
	ctx context.Context,
	operation func(artifact.UploadIntentTransaction) error,
) error {
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

	return mapUploadIntent(row)
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
		return &artifact.Error{Code: artifact.CodeConfirmationStale}
	}

	return nil
}

func (t *artifactTransaction) InsertVerificationArtifact(ctx context.Context, insertedArtifact artifact.VerificationArtifact) error {
	return t.queries.InsertVerificationArtifact(ctx, generated.InsertVerificationArtifactParams{
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
}

func (t *artifactTransaction) UpdateSessionState(ctx context.Context, sessionID uuid.UUID, expected verificationsession.State, next verificationsession.State, updatedAt time.Time) error {
	err := t.events.UpdateState(ctx, sessionID, expected, next, updatedAt)
	if errors.Is(err, session.ErrSessionTransitionStale) {
		return &artifact.Error{Code: artifact.CodeConfirmationStale}
	}
	return err
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
		return &artifact.Error{Code: artifact.CodeConfirmationStale}
	}

	return nil
}

func (t *artifactTransaction) AppendEvent(ctx context.Context, event session.AppendEventParams) error {
	return t.events.AppendEvent(ctx, event)
}

func (t *artifactTransaction) SupersedePendingUploadIntent(
	ctx context.Context,
	sessionID uuid.UUID,
	kind string,
	supersededAt time.Time,
) error {
	rowsAffected, err := t.queries.SupersedePendingUploadIntent(
		ctx,
		generated.SupersedePendingUploadIntentParams{
			SupersededAt:          supersededAt,
			VerificationSessionID: sessionID,
			Kind:                  kind,
		},
	)
	if err != nil {
		return err
	}
	if rowsAffected > 1 {
		return errors.New("multiple pending Upload Intents superseded")
	}
	return nil
}

func (t *artifactTransaction) InsertUploadIntent(
	ctx context.Context,
	intent artifact.UploadIntent,
) error {
	return t.queries.InsertUploadIntent(ctx, generated.InsertUploadIntentParams{
		UploadIntentID:        intent.ID,
		VerificationSessionID: intent.VerificationSessionID,
		Kind:                  intent.Kind,
		StorageKey:            intent.StorageKey,
		CreatedAt:             intent.CreatedAt,
		LatestStatusChangeAt:  intent.LatestStatusChangeAt,
		ExpiresAt:             intent.ExpiresAt,
	})
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

	return mapUploadIntent(row)
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

func mapUploadIntent(row generated.UploadIntent) (artifact.UploadIntent, error) {
	confirmedAt, err := timestamptzToTimePtr(row.ConfirmedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}
	objectDeletedAt, err := timestamptzToTimePtr(row.ObjectDeletedAt)
	if err != nil {
		return artifact.UploadIntent{}, err
	}
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
		FailureCode:           pgxTextToStringPtr(row.FailureCode),
	}, nil
}
