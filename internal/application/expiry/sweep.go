package expiry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

var ErrSweepPartialFailure = errors.New("partial failure on some sessions")

const maxConcurrency = 10

type DueSessionReader interface {
	FindDueSessionIDs(context.Context, time.Time, int) ([]uuid.UUID, error)
}

type Operation interface {
	Expire(context.Context, Input) (Result, error)
}

type SweepResult struct {
	Processed int
	Expired   int
	NoOp      int
	Failed    int
}

type SweepService struct {
	reader    DueSessionReader
	operation Operation
	clock     session.Clock
	batchSize int
	limiter   chan struct{}
}

func NewSweepService(
	reader DueSessionReader,
	operation Operation,
	clock session.Clock,
	batchSize int,
) *SweepService {
	return &SweepService{
		reader:    reader,
		operation: operation,
		clock:     clock,
		batchSize: batchSize,
		limiter:   make(chan struct{}, maxConcurrency),
	}
}

type outcomeState int

const (
	outcomeCancelled outcomeState = iota
	outcomeExpired
	outcomeNoop
	outcomeFailed
)

func (s *SweepService) Sweep(ctx context.Context) (SweepResult, error) {
	now := s.clock.Now()
	sessionIDs, err := s.reader.FindDueSessionIDs(ctx, now, s.batchSize)
	if err != nil {
		return SweepResult{}, err
	}

	if len(sessionIDs) == 0 {
		return SweepResult{}, nil
	}

	jobs := make(chan Input)
	outcomes := make(chan outcomeState)

	workerCount := min(maxConcurrency, len(sessionIDs))
	var wg sync.WaitGroup
	wg.Add(workerCount)

	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()

			for input := range jobs {
				state := s.processOne(ctx, input)
				outcomes <- state
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, sessionID := range sessionIDs {
			if err := ctx.Err(); err != nil {
				return
			}
			select {
			case jobs <- Input{
				SessionID: sessionID,
			}:
			case <-ctx.Done():
				return
			}

		}
	}()

	go func() {
		wg.Wait()
		close(outcomes)
	}()

	var sweepResult SweepResult
	for {
		outcomeState, ok := <-outcomes

		if !ok {
			break
		}
		switch outcomeState {
		case outcomeFailed:
			sweepResult.Failed++
		case outcomeNoop:
			sweepResult.NoOp++
		case outcomeExpired:
			sweepResult.Expired++
		case outcomeCancelled:
		}
	}

	sweepResult.Processed = sweepResult.Expired + sweepResult.NoOp + sweepResult.Failed

	if err := ctx.Err(); err != nil {
		return sweepResult, err
	}

	if sweepResult.Failed > 0 {
		return sweepResult, ErrSweepPartialFailure
	}

	return sweepResult, nil
}

func (s *SweepService) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.limiter <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *SweepService) release() {
	<-s.limiter
}

func (s *SweepService) processOne(ctx context.Context, input Input) outcomeState {
	if err := s.acquire(ctx); err != nil {
		return outcomeCancelled
	}
	defer s.release()

	if ctx.Err() != nil {
		return outcomeCancelled
	}

	result, err := s.operation.Expire(ctx, input)
	if err != nil {
		ctxErr := ctx.Err()
		if ctxErr != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return outcomeCancelled
		}

		if errors.Is(err, verificationsession.ErrTerminalState) {
			return outcomeNoop
		}

		return outcomeFailed
	}

	if !result.Changed {
		return outcomeNoop
	}
	return outcomeExpired
}
