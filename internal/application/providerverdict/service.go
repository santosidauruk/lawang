package providerverdict

import (
	"context"

	"github.com/santosidauruk/lawang-go/internal/application/session"
)

type Service struct {
	clock session.Clock
}

func NewProviderVerdictService(clock session.Clock) *Service {
	return &Service{
		clock: clock,
	}
}

func (s *Service) HandleVerifiedBody(_ context.Context, body []byte) error {
	return nil
}
