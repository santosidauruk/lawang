package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
)

type ConnLease interface {
	sqlc.DBTX
	transactionBeginner
	Release()
	Discard(context.Context) error
}

type AcquireFunc func(context.Context) (ConnLease, error)

type ArtifactConfirmCoordinator struct {
	acquireFunc AcquireFunc
}

func NewArtifactConfirmCoordinator(
	acquireFunc AcquireFunc,
) *ArtifactConfirmCoordinator {
	return &ArtifactConfirmCoordinator{
		acquireFunc: acquireFunc,
	}
}

func (c *ArtifactConfirmCoordinator) WithinConfirm(
	ctx context.Context,
	uploadIntentID uuid.UUID,
	operation func(artifact.Reader, artifact.Transactor) error,
) (resultErr error) {
	conn, err := c.acquireFunc(ctx)
	if err != nil {
		return err
	}

	queries := sqlc.New(conn)
	lockKey := confirmLockKey(uploadIntentID)

	if err := queries.AcquireArtifactConfirmLock(ctx, lockKey); err != nil {
		cleanupCtx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cancel()

		return errors.Join(err, conn.Discard(cleanupCtx))
	}

	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer unlockCancel()

		released, unlockErr := queries.ReleaseArtifactConfirmLock(
			unlockCtx,
			lockKey,
		)

		if unlockErr == nil && !released {
			unlockErr = errors.New(
				"artifact confirm advisory lock was not held",
			)
		}

		if unlockErr != nil {
			discardContext, discardCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer discardCancel()

			discardErr := conn.Discard(discardContext)
			resultErr = errors.Join(
				resultErr,
				unlockErr,
				discardErr,
			)
			return
		}

		conn.Release()
	}()

	scopedArtifacts := NewArtifactTransactions(conn)

	return operation(scopedArtifacts, scopedArtifacts)
}
