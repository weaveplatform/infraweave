package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Service implements vcsapi.Sources over the provider of each stack's
// repository.
type Service struct {
	stacks    stacksapi.Stacks
	providers map[string]port.Source
}

var _ vcsapi.Sources = (*Service)(nil)

// New returns the service reading stacks through stacks, with each
// provider's Source keyed by its served VCSProvider value.
func New(stacks stacksapi.Stacks, providers map[string]port.Source) *Service {
	return &Service{stacks: stacks, providers: providers}
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
	repo := port.Repository{Namespace: st.Namespace, Name: st.Repository}
	if st.RepositoryURL != nil {
		repo.URL = *st.RepositoryURL
	}
	return src, repo, nil
}
