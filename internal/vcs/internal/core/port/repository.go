package port

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// ErrRepositoryIdentity distinguishes a refused identity/path change from an
// operational failure while reading native metadata. Neither permits a crawl.
var ErrRepositoryIdentity = errors.New("vcs: repository identity or path changed")

// MatchRepository fences a recovery read before resolving any source object.
// Already reconciled renames supply the owner's current coordinates; an
// unobserved redirect cannot silently change preparation's fanout coordinates.
func MatchRepository(expected Repository, actual vcsapi.Repository) error {
	if expected.ExpectedID == "" {
		return nil
	}
	canonical := func(id string) string {
		if parsed, err := uuid.Parse(id); err == nil {
			return parsed.String()
		}
		return id
	}
	if canonical(expected.ExpectedID) != canonical(actual.ID) || !strings.EqualFold(expected.Namespace, actual.Namespace) || !strings.EqualFold(expected.Name, actual.Name) {
		return ErrRepositoryIdentity
	}
	return nil
}

// Resolution retains the provider identity alongside the resolved commit.
// Paths can change; the identity is what links a later event to a tracked stack.
type Resolution struct {
	Commit     vcsapi.Commit
	Repository vcsapi.Repository
}

// RepositorySource reads the native identity of an API-backed repository.
// Raw Git has no provider identity and implements Source alone.
type RepositorySource interface {
	Source
	ResolveRepositoryRef(context.Context, Repository, string) (Resolution, error)
}

// InstallationState is the provider's current App installation availability.
// It describes a provider owner and contains no local account identity.
type InstallationState struct {
	ID    string
	Owner vcsapi.Owner
	State string
}

// InstallationReader refreshes App availability without minting an installation
// token. Only providers with installations implement it.
type InstallationReader interface {
	ReadInstallations(context.Context, Integration) ([]InstallationState, error)
}
