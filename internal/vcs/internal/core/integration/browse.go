package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Git is the raw Git provider, whose stacks name their repository by URL
// and which lists branches but no repositories.
const Git = "GIT"

// Repositories lists the repositories that integration id reaches, or
// its provider's default when id is empty, sorted by namespace and name;
// filter, when it is not empty, keeps those whose namespace/name path
// contains it, ignoring case. An integration p cannot read, from a space
// it reads or one such a space inherits from, is ErrNotFound, as is a
// provider with no default when id is empty. Raw Git, which lists no
// repositories, is refused at /provider.
func (s *Service) Repositories(
	ctx context.Context,
	p kernel.Principal,
	provider, id, filter string,
) ([]port.Listed, error) {
	in, err := s.browsed(ctx, p, provider, id)
	if err != nil {
		return nil, err
	}
	b, ok := s.browse[in.Provider].(port.Browse)
	if !ok {
		return nil, unbrowsed(in.Provider)
	}
	all, err := b.Repositories(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // the provider names the read
	}
	filter = strings.ToLower(filter)
	return slices.DeleteFunc(all, func(l port.Listed) bool {
		return !strings.Contains(strings.ToLower(l.Namespace+"/"+l.Name), filter)
	}), nil
}

// Branches lists the branches of repo, sorted: on raw Git, the repository
// at repo.URL, refused at /repositoryURL when it is empty; on another
// provider, repo's namespace and name through integration id, or its
// provider's default when id is empty, which p reads as Repositories
// requires.
func (s *Service) Branches(
	ctx context.Context,
	p kernel.Principal,
	provider, id string,
	repo port.Repository,
) ([]string, error) {
	if provider == Git {
		if repo.URL == "" {
			return nil, &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
				Pointer: "/repositoryURL", Reason: kernel.ReasonRequired,
				Message: "A raw Git repository is named by its URL.",
			}}}
		}
		return s.branches(ctx, Git, repo)
	}
	in, err := s.browsed(ctx, p, provider, id)
	if err != nil {
		return nil, err
	}
	repo.Integration = &in
	return s.branches(ctx, in.Provider, repo)
}

// branches lists repo's branches through provider's Brancher.
func (s *Service) branches(
	ctx context.Context,
	provider string,
	repo port.Repository,
) ([]string, error) {
	b, ok := s.browse[provider]
	if !ok {
		return nil, unbrowsed(provider)
	}
	return b.Branches(ctx, repo) //nolint:wrapcheck // the provider names the read
}

// SearchUsers reads a page of the stacks in the spaces p may read that
// use integration id: those naming it, and, while it is its provider's
// default, those of its provider naming none.
func (s *Service) SearchUsers(
	ctx context.Context,
	p kernel.Principal,
	id string,
	in search.Input,
) (search.Result[stacksapi.Stack], error) {
	u, err := s.users(ctx, p, id)
	if err != nil {
		return search.Result[stacksapi.Stack]{}, err
	}
	//nolint:wrapcheck // the stacks domain names the read
	return s.stacks.SearchUsers(ctx, p, u, in)
}

// SuggestUsers lists the values the stacks SearchUsers reads hold.
func (s *Service) SuggestUsers(
	ctx context.Context,
	p kernel.Principal,
	id string,
	in search.SuggestionsInput,
) (search.Suggestions, error) {
	u, err := s.users(ctx, p, id)
	if err != nil {
		return search.Suggestions{}, err
	}
	//nolint:wrapcheck // the stacks domain names the read
	return s.stacks.SuggestUsers(ctx, p, u, in)
}

// users is how the stacks domain finds the stacks that use integration
// id of p's account.
func (s *Service) users(ctx context.Context, p kernel.Principal, id string) (stacksapi.Use, error) {
	in, err := s.get(ctx, p.AccountID, id, "")
	if err != nil {
		return stacksapi.Use{}, err
	}
	return stacksapi.Use{Integration: in.ID, Provider: in.Provider, Default: in.IsDefault}, nil
}

// browsed is the integration p browses, with its secrets opened: the
// operator's for a built-in provider; id, of provider when it is not
// empty; or provider's default when id is empty.
func (s *Service) browsed(
	ctx context.Context,
	p kernel.Principal,
	provider, id string,
) (port.Integration, error) {
	if use, ok := s.builtin[provider]; ok {
		return use, nil
	}
	if id == "" && provider == "" {
		return port.Integration{}, &kernel.Invalid{
			Detail: invalidDetail,
			Errors: []kernel.FieldError{
				{
					Pointer: "/provider",
					Reason:  kernel.ReasonRequired,
					Message: "Name the provider whose default integration to browse, or an integration.",
				},
			},
		}
	}
	if id == "" && !slices.Contains(s.providers, provider) {
		return port.Integration{}, unbrowsed(provider)
	}
	var in Integration
	var err error
	if id == "" {
		in, err = s.store.Default(ctx, p.AccountID, provider)
	} else {
		in, err = s.get(ctx, p.AccountID, id, provider)
	}
	if err != nil {
		return port.Integration{}, err
	}
	if err := s.visible(ctx, p, in); err != nil {
		return port.Integration{}, err
	}
	return s.use(ctx, in)
}

// visible is nil when p may read in's space or a space that inherits
// from it, as a stack there may bind it, and ErrNotFound otherwise.
func (s *Service) visible(ctx context.Context, p kernel.Principal, in Integration) error {
	reach, err := s.authz.Reachable(ctx, p, accessapi.ActionSpaceRead)
	if err != nil {
		return err //nolint:wrapcheck // the snapshot's read names itself
	}
	if slices.Contains(reach, in.Space) {
		return nil
	}
	tree, err := s.spaces.Tree(ctx, p.AccountID)
	if err != nil {
		return err //nolint:wrapcheck // the access domain names the read
	}
	for _, space := range reach {
		if slices.Contains(Reach(tree, space), in.Space) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotFound, in.ID)
}

// unbrowsed refuses provider, which lists nothing to browse.
func unbrowsed(provider string) error {
	return &kernel.Invalid{Detail: invalidDetail, Errors: []kernel.FieldError{{
		Pointer: "/provider", Reason: kernel.ReasonRule,
		Message: fmt.Sprintf("Provider %q lists no repositories to browse.", provider),
	}}}
}
