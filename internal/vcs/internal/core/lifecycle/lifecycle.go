package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
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

// Installation keeps provider ownership and availability, never a local account.
type Installation struct {
	ID         string
	Owner      vcsapi.Owner
	State      string
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
	PutMembership(context.Context, kernel.AccountID, kernel.ULID, string, Repository, bool) error
	MembershipRemoved(context.Context, kernel.AccountID, kernel.ULID, string, string) (bool, error)
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
	removed, err := s.Store.MembershipRemoved(ctx, acct, integration, namespace, name)
	if err != nil {
		return err
	}
	if removed {
		return &kernel.Invalid{Detail: fmt.Sprintf("The GitHub App installation on %s no longer reaches this repository.", namespace)}
	}
	installs, err := s.Store.Installations(ctx, acct, integration)
	if err != nil {
		return err
	}
	for _, in := range installs {
		if in.Owner.Login == namespace && in.State != "active" {
			return &kernel.Invalid{Detail: fmt.Sprintf("The GitHub App installation on %s is %s.", in.Owner.Login, in.State)}
		}
	}
	return nil
}

// Seed records provider-resolved identity; a removed membership cannot be
// restored by the stack.changed event emitted by an earlier rename.
func (s *Service) Seed(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, repo vcsapi.Repository) error {
	if repo.ID == "" || repo.Namespace == "" || repo.Name == "" {
		return nil
	}
	return tx.Do(ctx, s.DB, tx.Options{}, func(ctx context.Context) error {
		found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
		if err != nil || !found {
			return err
		}
		if err := s.Check(ctx, acct, id, repo.Namespace, repo.Name); err != nil {
			return err
		}
		return s.observe(ctx, acct, id, use, repo, s.Now())
	})
}

// Apply runs before fan-out selection in the delivery's completing transaction.
func (s *Service) Apply(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, events []vcsapi.Event) error {
	if !tx.In(ctx) {
		return queue.ErrNoTransaction
	}
	found, err := s.Store.HoldRepositoryIntegration(ctx, acct, id)
	if err != nil || !found {
		return err
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
			removed, err := s.Store.MembershipRemoved(ctx, acct, id, event.Repository.Namespace, event.Repository.Name)
			if err != nil {
				return err
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
		return err
	}
	if found && old.ObservedAt.After(at) {
		return nil
	}
	occupant, occupied, err := s.Store.RepositoryPath(ctx, acct, id, repo.Namespace, repo.Name)
	if err != nil {
		return err
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
			return err
		}
	}
	return s.Store.PutRepositoryIdentity(ctx, acct, id, Repository{Repository: repo, ObservedAt: at})
}

func (s *Service) renameOwner(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, owner vcsapi.OwnerRename) error {
	repos, err := s.Store.OwnerRepositories(ctx, acct, id, owner.ID, owner.PreviousLogin)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		repo.Namespace, repo.OwnerID = owner.Login, owner.ID
		if err := s.observe(ctx, acct, id, use, repo.Repository, at); err != nil {
			return err
		}
	}
	installs, err := s.Store.Installations(ctx, acct, id)
	if err != nil {
		return err
	}
	for _, in := range installs {
		if in.Owner.ID == owner.ID && !in.ObservedAt.After(at) {
			in.Owner.Login, in.ObservedAt = owner.Login, at
			if err := s.Store.PutInstallation(ctx, acct, id, in); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) install(ctx context.Context, acct kernel.AccountID, id kernel.ULID, use stacksapi.Use, at time.Time, event vcsapi.Installation) error {
	all, err := s.Store.Installations(ctx, acct, id)
	if err != nil {
		return err
	}
	state := "active"
	for _, in := range all {
		if in.ID == event.ID {
			if in.ObservedAt.After(at) {
				return nil
			}
			state = in.State
		}
	}
	switch event.Action {
	case "created", "unsuspend", "new_permissions_accepted":
		state = "active"
	case "suspend":
		state = "suspended"
	case "deleted":
		state = "deleted"
	}
	if err := s.Store.PutInstallation(ctx, acct, id, Installation{ID: event.ID, Owner: event.Owner, State: state, ObservedAt: at}); err != nil {
		return err
	}
	for _, repo := range append(event.Repositories, event.Added...) {
		repo.ID, repo.OwnerID = CanonicalID(repo.ID), event.Owner.ID
		if err := s.Store.PutMembership(ctx, acct, id, event.ID, Repository{repo, at}, true); err != nil {
			return err
		}
		if err := s.observe(ctx, acct, id, use, repo, at); err != nil {
			return err
		}
	}
	for _, repo := range event.Removed {
		repo.ID, repo.OwnerID = CanonicalID(repo.ID), event.Owner.ID
		if err := s.Store.PutMembership(ctx, acct, id, event.ID, Repository{repo, at}, false); err != nil {
			return err
		}
		if err := s.Store.DropRepositoryIdentity(ctx, acct, id, repo.ID); err != nil {
			return err
		}
	}
	return nil
}
