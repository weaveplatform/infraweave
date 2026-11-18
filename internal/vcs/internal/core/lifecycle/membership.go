package lifecycle

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// membership accepts presence and coordinates on independent clocks. Removing
// the map must not erase the newer owner/path fact retained in its tombstone.
func (s *Service) membership(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	use stacksapi.Use,
	in Installation,
	at time.Time,
	repo vcsapi.Repository,
	present bool,
) error {
	repo.ID, repo.OwnerID = CanonicalID(repo.ID), in.Owner.ID
	coordinatesAt := at
	member, found, err := s.Store.MembershipIdentity(ctx, acct, id, repo.ID)
	if err != nil {
		return err //nolint:wrapcheck // the store names the failed operation
	} //nolint:wrapcheck // store names the read
	if found && member.CoordinatesAt.After(coordinatesAt) {
		repo, coordinatesAt = member.Repository.Repository, member.CoordinatesAt
	}
	current, found, err := s.Store.RepositoryIdentity(ctx, acct, id, repo.ID)
	if err != nil {
		return err //nolint:wrapcheck // the store names the failed operation
	} //nolint:wrapcheck // store names the read
	if found && current.ObservedAt.After(coordinatesAt) {
		repo, coordinatesAt = current.Repository, current.ObservedAt
	}
	if in.Owner.ID == repo.OwnerID && in.Owner.Login != repo.Namespace && in.ObservedAt.After(coordinatesAt) {
		repo.Namespace, coordinatesAt = in.Owner.Login, in.ObservedAt
	}
	observation := Membership{Repository: Repository{Repository: repo, ObservedAt: at}, Present: present, CoordinatesAt: coordinatesAt}
	accepted, err := s.Store.PutMembership(ctx, acct, id, in.ID, observation)
	if err != nil {
		return err //nolint:wrapcheck // the store names the failed operation
	} //nolint:wrapcheck // store names the write
	if !accepted {
		return nil
	}
	if present {
		return s.observe(ctx, acct, id, use, repo, coordinatesAt)
	}
	return s.Store.DropRepositoryIdentity(ctx, acct, id, repo.ID) //nolint:wrapcheck // store names the write
}
