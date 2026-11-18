package port

import (
	"context"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// TagResolver peels a provider's immutable delivered tag object when its push
// contract carries an object ID rather than the target commit ID. It must never
// resolve the tag's mutable current name.
type TagResolver interface {
	ResolveTag(context.Context, Repository, string) (vcsapi.Commit, error)
}
