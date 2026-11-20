package port

import (
	"context"

	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

// RequestSnapshot is native metadata read from the target repository's request
// API. Optional decision facts remain unknown when the provider supplies none.
// Repository identities are validated before a source or target commit is read.
type RequestSnapshot struct {
	Request        vcsapi.PullRequest `json:"request"`
	Base           string             `json:"base"`
	Head           string             `json:"head"`
	BaseRepository vcsapi.Repository  `json:"baseRepository"`
	HeadRepository vcsapi.Repository  `json:"headRepository"`
	Approved       *bool              `json:"approved,omitempty"`
	Mergeable      *bool              `json:"mergeable,omitempty"`
	Undiverged     *bool              `json:"undiverged,omitempty"`
}

// RequestReader resolves an existing native pull/merge request. Source
// preparation calls it outside transactions through the integration's own
// provider; policy evaluation never calls a provider.
type RequestReader interface {
	ReadRequest(context.Context, Repository, int) (RequestSnapshot, error)
}

// RequestRevision is the immutable native request comparison. Base retains the
// target tip from the request snapshot; Files compares the source to its merge
// base, without trusting a provider's truncated comparison file list.
type RequestRevision struct {
	Base, Head vcsapi.Commit
	Files      []string
}

// RequestSource uses the original target's authority for request-specific
// source reads. GitHub needs this for accessible forks without an installation
// on the fork; it does not select another credential or integration.
type RequestSource interface {
	ResolveRequest(context.Context, Repository, RequestSnapshot) (RequestRevision, error)
}

// RequestFacts freezes the native snapshot and immutable compared commits in a
// stored prepared event. A retry reads these facts rather than a moving branch.
type RequestFacts struct {
	Snapshot RequestSnapshot  `json:"snapshot"`
	Base     stacksapi.Commit `json:"base"`
	Head     stacksapi.Commit `json:"head"`
	Files    []string         `json:"files"`
	Actor    string           `json:"actor"`
	Comment  *string          `json:"comment,omitempty"`
}
