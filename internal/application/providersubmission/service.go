package providersubmission

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type Transaction interface {
	LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error)
	HasRequiredAcceptedArtifacts(ctx context.Context, sessionID uuid.UUID) (bool, error)
	SetPendingVerification(ctx context.Context, submittedAt time.Time, sessionID uuid.UUID) error
	InsertUnpublishedOutbox(ctx context.Context, outboxID uuid.UUID, sessionID uuid.UUID) error
	AppendEvent(ctx context.Context, event session.AppendEventParams) error
}

type Transactions interface {
	WithinTransaction(ctx context.Context, operation func(t Transaction) error) error
}
