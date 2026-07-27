package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	generated "github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"

)
type transactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type EventTransactions struct {
	database transactionBeginner
	queries  *generated.Queries
}

func NewEventTransactions(database interface {
	transactionBeginner
	generated.DBTX
}) *EventTransactions {
	return &EventTransactions{database: database, queries: generated.New(database)}
}

func (t *EventTransactions) WithinTransaction(
	ctx context.Context,
	operation func(session.EventTransaction) error,
) error {
	tx, err := t.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := operation(&eventTransaction{queries: generated.New(tx)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (t *EventTransactions) ListEvents(ctx context.Context, sessionID uuid.UUID) ([]sessionevent.Event, error) {
	rows, err := t.queries.ListSessionEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	events := make([]sessionevent.Event, 0, len(rows))
	for _, row := range rows {
		eventType, err := sessionevent.ParseType(row.EventType)
		if err != nil {
			return nil, fmt.Errorf("map Session Event type: %w", err)
		}
		metadata, err := sessionevent.ParseMetadata(row.Metadata)
		if err != nil {
			return nil, fmt.Errorf("map Session Event metadata: %w", err)
		}
		events = append(events, sessionevent.Event{
			ID: row.ID, SessionID: row.SessionID, Type: eventType,
			Metadata: metadata, OccurredAt: row.OccurredAt,
		})
	}
	return events, nil
}

type eventTransaction struct {
	queries *generated.Queries
}

func (t *eventTransaction) UpdateState(
	ctx context.Context,
	id uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	updatedAt time.Time,
) error {
	rows, err := t.queries.GuardVerificationSessionState(
		ctx,
		generated.GuardVerificationSessionStateParams{
			ID: id, ExpectedState: expected.String(), NextState: next.String(), UpdatedAt: updatedAt,
		},
	)
	if err != nil {
		return err
	}
	if rows != 1 {
		return session.ErrSessionTransitionStale
	}
	return nil
}

func (t *eventTransaction) AppendEvent(ctx context.Context, params session.AppendEventParams) error {
	if _, err := sessionevent.ParseType(params.Type.String()); err != nil {
		return err
	}
	metadata, err := json.Marshal(params.Metadata)
	if err != nil {
		return err
	}
	_, err = t.queries.AppendSessionEvent(ctx, generated.AppendSessionEventParams{
		SessionID: params.SessionID, EventType: params.Type.String(), Metadata: metadata,
		OccurredAt: params.OccurredAt,
	})
	return err
}
