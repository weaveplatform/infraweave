package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Integrations finds the integration a stack uses now, with its secrets
// opened: its own, id, or its provider's default when id is nil; false
// when it names none and the provider has no default.
type Integrations interface {
	Use(
		ctx context.Context,
		acct kernel.AccountID,
		provider string,
		id *kernel.ULID,
	) (port.Integration, bool, error)
}

// Service implements vcsapi.Sources over the provider of each stack's
// repository.
type Service struct {
	stacks       stacksapi.Stacks
	providers    map[string]port.Source
	integrations Integrations
}

var _ vcsapi.Sources = (*Service)(nil)

// New returns the service reading stacks through stacks and their
// integrations through integrations, with each provider's Source keyed by
// its served VCSProvider value.
func New(
	stacks stacksapi.Stacks,
	providers map[string]port.Source,
	integrations Integrations,
) *Service {
	return &Service{stacks: stacks, providers: providers, integrations: integrations}
}

// Resolve implements vcsapi.Sources.
func (s *Service) Resolve(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	ref string,
) (vcsapi.Commit, error) {
	src, repo, err := s.source(ctx, acct, stack)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	c, err := src.ResolveRef(ctx, repo, ref)
	if errors.Is(err, port.ErrNotFound) {
		return vcsapi.Commit{}, fmt.Errorf("%w: %s", vcsapi.ErrRefNotFound, ref)
	}
	if errors.Is(err, port.ErrRepositoryURL) {
		return vcsapi.Commit{}, &kernel.Invalid{
			Detail: "The stack's repository URL is not one infraweave reaches.",
			Errors: []kernel.FieldError{
				{
					Pointer: "/repositoryURL",
					Reason:  kernel.ReasonRule,
					Message: "The repository URL must be an https URL without a credential, query or fragment.",
				},
			},
		}
	}
	if err != nil {
		return vcsapi.Commit{}, fmt.Errorf("vcs: resolve %s: %w", ref, err)
	}
	return c, nil
}

// Resolver is the service as the stacks domain's commit operations reach
// it, through stacksapi.StackSources.
func (s *Service) Resolver() stacksapi.StackSources { return resolver{s} }

type resolver struct{ s *Service }

// Resolve implements stacksapi.StackSources.
func (r resolver) Resolve(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	ref string,
) (stacksapi.Commit, error) {
	c, err := r.s.Resolve(ctx, acct, stack, ref)
	if errors.Is(err, vcsapi.ErrRefNotFound) {
		return stacksapi.Commit{}, fmt.Errorf("%w: %w", stacksapi.ErrRefNotFound, err)
	}
	return stackCommit(c), err
}

// stackCommit is c as the stacks domain keeps it.
func stackCommit(c vcsapi.Commit) stacksapi.Commit {
	return stacksapi.Commit{
		Hash: c.Hash, Tag: c.Tag, URL: c.URL, Message: c.Message, AuthorName: c.AuthorName,
		AuthorLogin: c.AuthorLogin, Timestamp: c.Timestamp,
	}
}

// Track sets acct's stack id's branch head and tracked commit to the
// head of its branch, set by infraweave. A stack deleted since, or on a
// provider infraweave reads no source of, has nothing to track; a branch
// the repository lacks is a queue.Permanent failure, which no retry
// mends, and the stack keeps what it tracked.
func (s *Service) Track(ctx context.Context, acct kernel.AccountID, stack kernel.ULID) error {
	src, repo, err := s.source(ctx, acct, stack)
	if _, ok := errors.AsType[*kernel.Invalid](err); ok || errors.Is(err, stacksapi.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := s.stacks.Settings(ctx, acct, stack)
	if err != nil {
		return err //nolint:wrapcheck // stacks names the read
	}
	c, err := src.ResolveRef(ctx, repo, st.Branch)
	if errors.Is(err, port.ErrNotFound) || errors.Is(err, port.ErrRepositoryURL) {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("vcs: track %s of stack %s: %w", st.Branch, stack, err))
	}
	if err != nil {
		return fmt.Errorf("vcs: track %s of stack %s: %w", st.Branch, stack, err)
	}
	head := stackCommit(c)
	//nolint:wrapcheck // stacks names the write
	return s.stacks.SetTracked(ctx, acct, stack, &head, &head, "infraweave")
}

// SourceURL implements vcsapi.Sources.
func (s *Service) SourceURL(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	commit string,
) (string, error) {
	src, repo, err := s.source(ctx, acct, stack)
	if err != nil {
		return "", err
	}
	return src.SourceURL(repo, commit), nil
}

// source is the Source of the provider acct's stack id names, and the
// stack's repository as it reaches it.
func (s *Service) source(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
) (port.Source, port.Repository, error) {
	st, err := s.stacks.Settings(ctx, acct, stack)
	if err != nil {
		return nil, port.Repository{}, err //nolint:wrapcheck // stacks names the read
	}
	src, ok := s.providers[st.Provider]
	if !ok {
		return nil, port.Repository{}, &kernel.Invalid{
			Detail: fmt.Sprintf(
				"The stack's provider %s has no source infraweave reads.",
				st.Provider,
			),
			Errors: []kernel.FieldError{{
				Pointer: "/provider",
				Reason:  kernel.ReasonRule,
				Message: fmt.Sprintf("Provider %s has no source infraweave reads.", st.Provider),
			}},
		}
	}
	if st.VCSDetached {
		return nil, port.Repository{}, &kernel.Invalid{
			Detail: "The stack's VCS integration was deleted.",
		}
	}
	repo := port.Repository{Namespace: st.Namespace, Name: st.Repository}
	if st.RepositoryURL != nil {
		repo.URL = *st.RepositoryURL
	}
	in, ok, err := s.integrations.Use(ctx, acct, st.Provider, st.VCSIntegration)
	if err != nil {
		return nil, port.Repository{}, err //nolint:wrapcheck // the store names the read
	}
	if ok {
		repo.Integration = &in
	}
	return src, repo, nil
}
