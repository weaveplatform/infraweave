package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// Repository is the last unambiguous native identity/path observation.
type Repository struct {
	vcsapi.Repository
	ObservedAt time.Time
}

// Membership retains availability independently of the current map row.
type Membership struct {
	Repository
	Present bool
}

// Installation keeps provider ownership and availability, never a local account.
type Installation struct {
	ID         string
	Owner      vcsapi.Owner
	State      string
	Current    bool
	ObservedAt time.Time
}

// Store serializes lifecycle changes on the live integration before touching its
// owned rows. Every write joins the caller's transaction.
type Store interface {
	HoldRepositoryIntegration(context.Context, kernel.AccountID, kernel.ULID) (bool, error)
	RepositoryIdentity(context.Context, kernel.AccountID, kernel.ULID, string) (Repository, bool, error)
	RepositoryPath(context.Context, kernel.AccountID, kernel.ULID, string, string) (Repository, bool, error)
	OwnerRepositories(context.Context, kernel.AccountID, kernel.ULID, string, string) ([]Repository, error)
	PutRepositoryIdentity(context.Context, kernel.AccountID, kernel.ULID, Repository) error
	DropRepositoryIdentity(context.Context, kernel.AccountID, kernel.ULID, string) error
	Installations(context.Context, kernel.AccountID, kernel.ULID) ([]Installation, error)
	PutInstallation(context.Context, kernel.AccountID, kernel.ULID, Installation) error
	PutMembership(context.Context, kernel.AccountID, kernel.ULID, string, Repository, bool) (bool, error)
	MembershipRemoved(context.Context, kernel.AccountID, kernel.ULID, string, string, string) (bool, error)
	MoveMembership(context.Context, kernel.AccountID, kernel.ULID, Repository) error
	OwnerMemberships(context.Context, kernel.AccountID, kernel.ULID, string, string) ([]Membership, error)
}

// Service owns repository renames and native installation availability.
type Service struct {
	DB     tx.Beginner
	Store  Store
	Stacks stacksapi.Stacks
	Now    func() time.Time
	Log    *slog.Logger
}

// CanonicalID equates the UUID spellings provider APIs use without inventing
// equivalence for opaque or numeric IDs.
func CanonicalID(id string) string {
	if parsed, err := uuid.Parse(id); err == nil {
		return parsed.String()
	}
	return id
}

// Check fails only when durable provider observations establish unavailability.
// Unknown installations retain the ordinary provider lookup for new bindings.
func (s *Service) Check(ctx context.Context, acct kernel.AccountID, integration kernel.ULID, namespace, name string) error {
	removed, err := s.Store.MembershipRemoved(ctx, acct, integration, namespace, name, "")
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	if removed {
		return &kernel.Invalid{Detail: fmt.Sprintf("The GitHub App installation on %s no longer reaches this repository.", namespace)}
	}
	repository, known, err := s.Store.RepositoryPath(ctx, acct, integration, namespace, name)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	installs, err := s.Store.Installations(ctx, acct, integration)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	for _, in := range installs {
		if (in.Owner.Login == namespace || known && repository.OwnerID != "" && in.Owner.ID == repository.OwnerID) && in.Current && in.State != "active" {
			return &kernel.Invalid{Detail: fmt.Sprintf("The GitHub App installation on %s is %s.", in.Owner.Login, in.State)}
		}
	}
	return nil
}

// Seed records provider-resolved identity; a removed membership cannot be
// restored by the stack.changed event emitted by an earlier rename.
func (s *Service) Seed(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, repo vcsapi.Repository) error {
	return s.SeedAt(ctx, acct, id, use, repo, s.Now())
}

// SeedAt uses the time before the provider read, so waiting for a database lock
// cannot make an older API observation newer than a concurrent rename delivery.
func (s *Service) SeedAt(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, repo vcsapi.Repository, at time.Time) error {
	if repo.ID == "" || repo.Namespace == "" || repo.Name == "" {
		return nil
	}
	return tx.Do(ctx, s.DB, tx.Options{}, func(ctx context.Context) error { //nolint:wrapcheck // the called boundary names the failed operation
		found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
		if err != nil || !found {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
		if err := s.Check(ctx, acct, id, repo.Namespace, repo.Name); err != nil {
			return err
		}
		return s.observe(ctx, acct, id, use, repo, at)
	})
}

// Apply runs before fan-out selection in the delivery's completing transaction.
func (s *Service) Apply(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, events []vcsapi.Event) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
	if err != nil || !found {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	for _, event := range events {
		if event.OwnerRename != nil {
			if err := s.renameOwner(ctx, acct, id, use, at, *event.OwnerRename); err != nil {
				return err
			}
		}
		if event.Installation != nil {
			if err := s.install(ctx, acct, id, use, at, *event.Installation); err != nil {
				return err
			}
		}
		if event.Repository.ID != "" && event.Repository.Namespace != "" && event.Repository.Name != "" {
			removed, err := s.Store.MembershipRemoved(ctx, acct, id, event.Repository.Namespace, event.Repository.Name, CanonicalID(event.Repository.ID))
			if err != nil {
				return err //nolint:wrapcheck // the called boundary names the failed operation
			}
			if !removed {
				if err := s.observe(ctx, acct, id, use, event.Repository, at); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) observe(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, repo vcsapi.Repository, at time.Time) error {
	repo.ID = CanonicalID(repo.ID)
	old, found, err := s.Store.RepositoryIdentity(ctx, acct, id, repo.ID)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	if found && old.ObservedAt.After(at) {
		return nil
	}
	occupant, occupied, err := s.Store.RepositoryPath(ctx, acct, id, repo.Namespace, repo.Name)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	if occupied && occupant.ID != repo.ID {
		if s.Log != nil {
			s.Log.WarnContext(ctx, "vcs: repository path belongs to another native identity", "integration", id, "repository", repo.ID)
		}
		return nil
	}
	if repo.OwnerID == "" && found {
		repo.OwnerID = old.OwnerID
	}
	if found && (old.Namespace != repo.Namespace || old.Name != repo.Name) {
		if err := s.Stacks.RepointVCSRepository(ctx, acct, use,
			stacksapi.VCSRepository{Namespace: old.Namespace, Name: old.Name},
			stacksapi.VCSRepository{Namespace: repo.Namespace, Name: repo.Name}); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
	}
	observation := Repository{Repository: repo, ObservedAt: at}
	if err := s.Store.PutRepositoryIdentity(ctx, acct, id, observation); err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	return s.Store.MoveMembership(ctx, acct, id, observation) //nolint:wrapcheck // the called boundary names the failed operation
}

func (s *Service) renameOwner(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, owner vcsapi.OwnerRename) error {
	members, err := s.Store.OwnerMemberships(ctx, acct, id, owner.ID, owner.PreviousLogin)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	repos, err := s.Store.OwnerRepositories(ctx, acct, id, owner.ID, owner.PreviousLogin)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	for _, repo := range repos {
		repo.Namespace, repo.OwnerID = owner.Login, owner.ID
		if err := s.observe(ctx, acct, id, use, repo.Repository, at); err != nil {
			return err
		}
	}
	// Removed repositories have no current map entry, but their stacks and
	// membership tombstone still follow the stable native owner.
	for _, member := range members {
		if member.Present || member.ObservedAt.After(at) {
			continue
		}
		occupied, found, err := s.Store.RepositoryPath(ctx, acct, id, owner.Login, member.Name)
		if err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
		if found && occupied.ID != member.ID {
			continue
		}
		if err := s.Stacks.RepointVCSRepository(ctx, acct, use, stacksapi.VCSRepository{Namespace: member.Namespace, Name: member.Name},
			stacksapi.VCSRepository{Namespace: owner.Login, Name: member.Name}); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
		member.Namespace, member.ObservedAt = owner.Login, at
		if err := s.Store.MoveMembership(ctx, acct, id, member.Repository); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
	}
	installs, err := s.Store.Installations(ctx, acct, id)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	for _, in := range installs {
		if in.Owner.ID == owner.ID && !in.ObservedAt.After(at) {
			in.Owner.Login, in.ObservedAt = owner.Login, at
			if err := s.Store.PutInstallation(ctx, acct, id, in); err != nil {
				return err //nolint:wrapcheck // the called boundary names the failed operation
			}
		}
	}
	return nil
}

func (s *Service) install(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, event vcsapi.Installation) error {
	all, err := s.Store.Installations(ctx, acct, id)
	if err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	}
	next, apply := nextInstallation(all, event, at)
	if !apply {
		return nil
	}
	if err := s.Store.PutInstallation(ctx, acct, id, next); err != nil {
		return err //nolint:wrapcheck // the called boundary names the failed operation
	} //nolint:wrapcheck // store names the write
	if !next.Current {
		return nil
	}
	for _, repo := range slices.Concat(event.Repositories, event.Added) {
		repo.ID, repo.OwnerID = CanonicalID(repo.ID), event.Owner.ID
		current, found, err := s.Store.RepositoryIdentity(ctx, acct, id, repo.ID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the failed read
		} //nolint:wrapcheck // store names the read
		if found && current.ObservedAt.After(at) {
			repo = current.Repository
		}
		if accepted, err := s.Store.PutMembership(ctx, acct, id, event.ID, Repository{repo, at}, true); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		} else if !accepted {
			continue
		}
		if err := s.observe(ctx, acct, id, use, repo, at); err != nil {
			return err
		}
	}
	for _, repo := range event.Removed {
		repo.ID, repo.OwnerID = CanonicalID(repo.ID), event.Owner.ID
		current, found, err := s.Store.RepositoryIdentity(ctx, acct, id, repo.ID)
		if err != nil {
			return err //nolint:wrapcheck // the store names the failed read
		} //nolint:wrapcheck // store names the read
		if found && current.ObservedAt.After(at) {
			repo = current.Repository
		}
		if accepted, err := s.Store.PutMembership(ctx, acct, id, event.ID, Repository{repo, at}, false); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		} else if !accepted {
			continue
		}
		if err := s.Store.DropRepositoryIdentity(ctx, acct, id, repo.ID); err != nil {
			return err //nolint:wrapcheck // the called boundary names the failed operation
		}
	}
	return nil
}

// EventRepository reaches the current native path when an older delivery is
// replayed after a later rename. The original event remains the lifecycle fact;
// only its immutable source facts/fan-out use the newer known coordinates.
func (s *Service) EventRepository(ctx context.Context, acct kernel.AccountID, id kernel.ULID, at time.Time, repo vcsapi.Repository) (vcsapi.Repository, error) {
	if repo.ID == "" {
		return repo, nil
	}
	current, found, err := s.Store.RepositoryIdentity(ctx, acct, id, CanonicalID(repo.ID))
	if err != nil {
		return vcsapi.Repository{}, err //nolint:wrapcheck // the called boundary names the failed operation
	}
	if found && current.ObservedAt.After(at) {
		return current.Repository, nil
	}
	return repo, nil
}

// FanoutRepository runs under the integration fence, after Apply. A refused
// path collision produces no target instead of routing this native ID to the
// unrelated repository already occupying the event's advertised coordinates.
func (s *Service) FanoutRepository(ctx context.Context, acct kernel.AccountID, id kernel.ULID, at time.Time, repo vcsapi.Repository) (vcsapi.Repository, bool, error) {
	if !tx.In(ctx) {
		return vcsapi.Repository{}, false, queue.ErrNoTransaction
	}
	current, found, err := s.Store.RepositoryIdentity(ctx, acct, id, CanonicalID(repo.ID))
	if err != nil {
		return vcsapi.Repository{}, false, err //nolint:wrapcheck // the called boundary names the failed operation
	} //nolint:wrapcheck // store names the read
	if !found {
		return vcsapi.Repository{}, false, nil
	}
	if current.Namespace != repo.Namespace || current.Name != repo.Name {
		if !current.ObservedAt.After(at) {
			return vcsapi.Repository{}, false, nil
		}
	}
	return current.Repository, true, nil
}
