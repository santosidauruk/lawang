package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	generated "github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type SessionStore struct {
	queries *generated.Queries
}

func NewSessionStore(database generated.DBTX) *SessionStore {
	return &SessionStore{queries: generated.New(database)}
}

func (s *SessionStore) Create(ctx context.Context, params session.CreateParams) (session.VerificationSession, error) {
	row, err := s.queries.CreateVerificationSession(ctx, generated.CreateVerificationSessionParams{
		ResumeTokenHash: params.ResumeTokenHash,
		ExpiresAt:       params.ExpiresAt,
	})
	if err != nil {
		return session.VerificationSession{}, err
	}
	return session.VerificationSession{
		ID: row.ID, Status: session.Status(row.Status), ResumeTokenHash: row.ResumeTokenHash,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func (s *SessionStore) FindByID(ctx context.Context, id uuid.UUID) (session.VerificationSession, error) {
	row, err := s.queries.GetVerificationSessionByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	if err != nil {
		return session.VerificationSession{}, err
	}
	return session.VerificationSession{
		ID: row.ID, Status: session.Status(row.Status), ResumeTokenHash: row.ResumeTokenHash,
		ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
