package api

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ReviewMetadataReader supplies owner-backed metadata for an authorized review.
// The result joins the review transaction and is recorded once, never refreshed
// from the reviewer's later roles or session. It grants no permission itself.
type ReviewMetadataReader interface {
	ReviewMetadata(context.Context, kernel.Principal, kernel.ULID) (ReviewMetadata, error)
}

// ReviewMetadata is a credential-free snapshot. AuthorKey identifies a person
// across their browser, CLI and personal-key sessions, or one account API key.
type ReviewMetadata struct {
	AuthorKey  string
	Session    kernel.Principal
	Roles      []ReviewRole
	Identity   *ReviewIdentity
	CreatorIP  *string
	IDPSubject *string
	// CanReview is the fresh decision in the same snapshot as Roles. It
	// prevents cached route authorization from admitting a revoked reviewer.
	CanReview bool
}

// ReviewRole retains the immutable role ID and its current public description.
// The public ID is Slug; ID is the separate immutable ULID.
type ReviewRole struct {
	ID         kernel.ULID
	Slug, Name string
}

// ReviewIdentity identifies the admitted member, rather than the credential
// acting for them. Account keys with no member have no person identity.
type ReviewIdentity struct {
	Member      kernel.MemberID
	Federations []ReviewFederation
}

// ReviewFederation retains actual provider IDs and stable subjects. A provider
// username is unavailable until its owner records it; it is never inferred from
// the member's login.
type ReviewFederation struct {
	Provider kernel.ULID
	Subject  string
}
