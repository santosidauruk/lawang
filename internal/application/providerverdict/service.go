package providerverdict

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verdict"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

type InsertWebhookEventOutcome uint8

const (
	WebhookEventInserted InsertWebhookEventOutcome = iota + 1
	WebhookEventDuplicate
)

type IgnoreReason string

const (
	IgnoreReasonUnknownSession IgnoreReason = "unknown_session"
)

type Transaction interface {
	InsertWebhookEvent(ctx context.Context, eventID uuid.UUID, sessionID uuid.UUID, payload []byte, receivedAt time.Time) (InsertWebhookEventOutcome, error)
	LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error)
	VerifySession(ctx context.Context, verifiedAt time.Time, sessionID uuid.UUID) error
	RejectSession(ctx context.Context, rejectedAt time.Time, sessionID uuid.UUID, rejectionReason verdict.RejectionReason) error
	AppendEvent(ctx context.Context, event session.AppendEventParams) error
	MarkEventApplied(ctx context.Context, webhookID uuid.UUID, processedAt time.Time) error
	MarkEventIgnored(ctx context.Context, webhookID uuid.UUID, processedAt time.Time, reason IgnoreReason) error
}

type Transactor interface {
	WithinTransaction(ctx context.Context, operation func(t Transaction) error) error
}

type Service struct {
	clock        session.Clock
	transactions Transactor
}

func NewProviderVerdictService(clock session.Clock, transactions Transactor) *Service {
	return &Service{
		clock:        clock,
		transactions: transactions,
	}
}

type ApplyInput struct {
	EventID           uuid.UUID
	ReportedSessionID uuid.UUID
	ProviderVerdict   verdict.ProviderVerdict
	RawPayload        []byte
	ReceivedAt        time.Time
}

func (s *Service) HandleVerifiedBody(_ context.Context, body []byte) (ApplyInput, error) {
	payload, err := decodeWebhookPayload(body)
	if err != nil {
		return ApplyInput{}, err
	}

	eventID, err := uuid.Parse(payload.EventID)
	if err != nil {
		return ApplyInput{}, errors.New("invalid eventId")
	}

	sessionID, err := uuid.Parse(payload.SessionID)
	if err != nil {
		return ApplyInput{}, errors.New("invalid sessionId")
	}

	reason, err := decodeOptionalReason(payload.Reason)
	if err != nil {
		return ApplyInput{}, err
	}

	providerVerdict, err := verdict.ParseProviderVerdict(payload.Verdict, reason)
	if err != nil {
		return ApplyInput{}, err
	}

	return ApplyInput{
		EventID:           eventID,
		ReportedSessionID: sessionID,
		ProviderVerdict:   providerVerdict,
		RawPayload:        body,
		ReceivedAt:        s.clock.Now(),
	}, nil
}

type webhookPayload struct {
	EventID   string          `json:"eventId"`
	SessionID string          `json:"sessionId"`
	Verdict   string          `json:"verdict"`
	Reason    json.RawMessage `json:"reason"`
}

func decodeWebhookPayload(body []byte) (webhookPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var payload webhookPayload
	if err := decoder.Decode(&payload); err != nil {
		return webhookPayload{}, fmt.Errorf("decode webhook payload: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return webhookPayload{}, errors.New("webhook body must contain exactly one JSON value")
		}
		return webhookPayload{}, fmt.Errorf("decode trailing webhook data: %w", err)
	}

	return payload, nil
}

func decodeOptionalReason(raw json.RawMessage) (*string, error) {
	if raw == nil {
		return nil, nil
	}

	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("reason must be a JSON string")
	}

	var reason string
	if err := json.Unmarshal(raw, &reason); err != nil {
		return nil, errors.New("reason must be a JSON string")
	}

	return &reason, nil
}

var ErrDuplicateWebhookEvent = errors.New("duplicate Webhook Event")

//  12. `[application service]` User menulis `VerdictService.Apply` verified transaction:
//     insert first event, lock/re-read session, guard pending, update terminal fields,
//     append `verification_passed`, dan mark event applied; duplicate marker belum
//     ditangani menjadi replay sampai Checkpoint 8.
func (s *Service) Apply(ctx context.Context, input ApplyInput) error {
	err := s.transactions.WithinTransaction(ctx, func(t Transaction) error {
		insertedOutcome, err := t.InsertWebhookEvent(ctx, input.EventID, input.ReportedSessionID, input.RawPayload, input.ReceivedAt)
		if err != nil {
			return err
		}
		if insertedOutcome == WebhookEventDuplicate {
			return ErrDuplicateWebhookEvent
		}

		now := s.clock.Now()

		lockedSession, err := t.LockSession(ctx, input.ReportedSessionID)
		if errors.Is(err, session.ErrSessionNotFound) {
			markErr := t.MarkEventIgnored(ctx, input.EventID, now, IgnoreReasonUnknownSession)
			if markErr != nil {
				return errors.Join(markErr, err)
			}
			return nil
		}

		if err != nil {
			return err
		}

		inputVerdict := input.ProviderVerdict.Verdict()

		switch inputVerdict {
		case verdict.Verified:
			_, err := verificationsession.Transition(lockedSession.Status, sessionevent.VerificationPassed)
			if err != nil {
				return err
			}
			err = t.VerifySession(ctx, now, input.ReportedSessionID)
			if err != nil {
				return err
			}

			err = t.AppendEvent(ctx, session.AppendEventParams{
				SessionID:  input.ReportedSessionID,
				Type:       sessionevent.VerificationPassed,
				Metadata:   sessionevent.EmptyMetadata(),
				OccurredAt: now,
			})
			if err != nil {
				return err
			}
		case verdict.Rejected:
			_, err := verificationsession.Transition(lockedSession.Status, sessionevent.VerificationFailed)
			if err != nil {
				return err
			}

			rejectionReason, ok := input.ProviderVerdict.RejectionReason()
			if !ok {
				return fmt.Errorf("rejection reason invalid: %s", rejectionReason)
			}
			err = t.RejectSession(ctx, now, input.ReportedSessionID, rejectionReason)
			if err != nil {
				return err
			}
			err = t.AppendEvent(ctx, session.AppendEventParams{
				SessionID:  input.ReportedSessionID,
				Type:       sessionevent.VerificationFailed,
				Metadata:   sessionevent.EmptyMetadata(),
				OccurredAt: now,
			})
			if err != nil {
				return err
			}
		default:
			return errors.New("invalid verdict")
		}

		err = t.MarkEventApplied(ctx, input.EventID, now)
		if err != nil {
			return err
		}

		return nil

	})
	if err != nil {
		return err
	}
	return nil
}
