package core

import (
	"context"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var _ configapi.PolicyContextReader = (*Service)(nil)

// PolicyContextSlugs uses the environment's selection without reading elements.
func (s *Service) PolicyContextSlugs(ctx context.Context, account kernel.AccountID, stack kernel.ULID) ([]string, error) {
	sources, _, err := s.sourceSelection(ctx, account, stack)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(sources)-1)
	for _, src := range sources[1:] {
		out = append(out, src.context)
	}
	return out, nil
}
