package port

import (
	"context"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

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
