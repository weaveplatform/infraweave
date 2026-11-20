package pool

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	workersapi "github.com/weaveplatform/infraweave/internal/workers/api"
)

var _ workersapi.PoolMetadataReader = (*Service)(nil)

// PoolMetadata reads only account-owned private-pool metadata. Credentials and
// certificates remain inside the pool owner and never enter policy input.
func (s *Service) PoolMetadata(ctx context.Context, account kernel.AccountID, id kernel.ULID) (workersapi.PoolMetadata, error) {
	p, err := s.store.Get(ctx, account, id, false)
	if errors.Is(err, ErrNotFound) {
		return workersapi.PoolMetadata{}, fmt.Errorf("%w: %s", workersapi.ErrNoPool, id)
	}
	if err != nil {
		return workersapi.PoolMetadata{}, err //nolint:wrapcheck // the owned store names the read
	}
	return workersapi.PoolMetadata{ID: p.ID, Space: p.Space, Name: p.Name, Labels: append([]string{}, p.Labels...), Public: false}, nil
}
