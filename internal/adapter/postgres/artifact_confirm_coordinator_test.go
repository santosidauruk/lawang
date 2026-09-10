package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santosidauruk/lawang/internal/application/artifact"
)

func TestArtifactConfirmCoordinatorDiscardsConnectionWhenUnlockFails(t *testing.T) {
	unlockErr := errors.New("injected unlock failure")
	lease := &coordinatorLease{unlockErr: unlockErr}
	coordinator := NewArtifactConfirmCoordinator(func(context.Context) (ConnLease, error) {
		return lease, nil
	})

	err := coordinator.WithinConfirm(
		context.Background(),
		uuid.MustParse("5cb8285e-9144-4abe-b1e8-e767c775c648"),
		func(artifact.Reader, artifact.Transactor) error { return nil },
	)

	if !errors.Is(err, unlockErr) {
		t.Fatalf("WithinConfirm() error = %v, want unlock error", err)
	}
	if lease.releaseCalls != 0 {
		t.Errorf("Release() calls = %d, want 0", lease.releaseCalls)
	}
	if lease.discardCalls != 1 {
		t.Errorf("Discard() calls = %d, want 1", lease.discardCalls)
	}
	if lease.discardContextErr != nil {
		t.Errorf("Discard() context error = %v, want nil", lease.discardContextErr)
	}
}

func TestArtifactConfirmCoordinatorUnlocksAndReleasesWhenOperationFails(t *testing.T) {
	operationErr := errors.New("injected operation failure")
	lease := &coordinatorLease{unlockResult: true}
	coordinator := NewArtifactConfirmCoordinator(func(context.Context) (ConnLease, error) {
		return lease, nil
	})

	err := coordinator.WithinConfirm(
		context.Background(),
		uuid.MustParse("1cc0607c-87be-40ff-90e4-ab12b97106d2"),
		func(artifact.Reader, artifact.Transactor) error { return operationErr },
	)

	if !errors.Is(err, operationErr) {
		t.Fatalf("WithinConfirm() error = %v, want operation error", err)
	}
	if lease.releaseCalls != 1 {
		t.Errorf("Release() calls = %d, want 1", lease.releaseCalls)
	}
	if lease.discardCalls != 0 {
		t.Errorf("Discard() calls = %d, want 0", lease.discardCalls)
	}
}

func TestArtifactConfirmCoordinatorDiscardsConnectionWhenLockAcquireFails(t *testing.T) {
	acquireErr := errors.New("injected advisory lock failure")
	lease := &coordinatorLease{acquireErr: acquireErr}
	coordinator := NewArtifactConfirmCoordinator(func(context.Context) (ConnLease, error) {
		return lease, nil
	})

	err := coordinator.WithinConfirm(
		context.Background(),
		uuid.MustParse("b56fd008-b89a-4353-af0c-12fd5cc0b6fc"),
		func(artifact.Reader, artifact.Transactor) error {
			t.Fatal("operation called after advisory lock acquisition failed")
			return nil
		},
	)

	if !errors.Is(err, acquireErr) {
		t.Fatalf("WithinConfirm() error = %v, want acquire error", err)
	}
	if lease.releaseCalls != 0 {
		t.Errorf("Release() calls = %d, want 0", lease.releaseCalls)
	}
	if lease.discardCalls != 1 {
		t.Errorf("Discard() calls = %d, want 1", lease.discardCalls)
	}
}

func TestArtifactConfirmCoordinatorDiscardsConnectionWhenUnlockReportsNotHeld(t *testing.T) {
	lease := &coordinatorLease{unlockResult: false}
	coordinator := NewArtifactConfirmCoordinator(func(context.Context) (ConnLease, error) {
		return lease, nil
	})

	err := coordinator.WithinConfirm(
		context.Background(),
		uuid.MustParse("9aff16d2-58a6-483a-a80b-f2110198e7db"),
		func(artifact.Reader, artifact.Transactor) error { return nil },
	)

	if err == nil {
		t.Fatal("WithinConfirm() error = nil, want unlock-not-held error")
	}
	if lease.releaseCalls != 0 {
		t.Errorf("Release() calls = %d, want 0", lease.releaseCalls)
	}
	if lease.discardCalls != 1 {
		t.Errorf("Discard() calls = %d, want 1", lease.discardCalls)
	}
}

func TestConfirmLockKeyHashesRawUUIDBytesIntoSignedBigEndianInt64(t *testing.T) {
	uploadIntentID := uuid.MustParse("5cb8285e-9144-4abe-b1e8-e767c775c648")

	got := confirmLockKey(uploadIntentID)

	const want int64 = -5458873089758345937
	if got != want {
		t.Errorf("confirmLockKey(%s) = %d, want %d", uploadIntentID, got, want)
	}
}

type coordinatorLease struct {
	acquireErr        error
	unlockResult      bool
	unlockErr         error
	releaseCalls      int
	discardCalls      int
	discardContextErr error
}

func (l *coordinatorLease) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, l.acquireErr
}

func (l *coordinatorLease) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (l *coordinatorLease) QueryRow(context.Context, string, ...any) pgx.Row {
	return coordinatorRow{released: l.unlockResult, err: l.unlockErr}
}

func (l *coordinatorLease) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected Begin call")
}

func (l *coordinatorLease) Release() {
	l.releaseCalls++
}

func (l *coordinatorLease) Discard(ctx context.Context) error {
	l.discardCalls++
	l.discardContextErr = ctx.Err()
	return nil
}

type coordinatorRow struct {
	released bool
	err      error
}

func (r coordinatorRow) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(destinations) != 1 {
		return errors.New("unexpected destination count")
	}
	released, ok := destinations[0].(*bool)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*released = r.released
	return nil
}
