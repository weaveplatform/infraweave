package linking

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TTL is how long a link waits for its two sign-ins.
//
// ref: decision 0006 § "Members and identity providers"
const TTL = time.Hour

// The organization trail's actions of identity linking, and the resource
// types they affect.
const (
	ActionCreate     = "member.link_create"
	ActionMerge      = "member.merge"
	ActionUnlink     = "identity.unlink"
	ResourceMember   = "MEMBER"
	ResourceIdentity = "IDENTITY"
)

var (
	// ErrLinkExists refuses a link of a pair that has an open link.
	ErrLinkExists = errors.New("the members have an open link")
	// ErrEnded refuses a sign-in through a link that expired or completed.
	ErrEnded = errors.New("the link has ended")
	// ErrNotLinked refuses a link's sign-in whose identity is neither
	// member's, or no member's.
	ErrNotLinked = errors.New("the identity is neither linked member's")
	// ErrOtherBrowser refuses a link's second sign-in from a browser that
	// does not hold the session of the first.
	ErrOtherBrowser = errors.New("the link's first sign-in was in another browser")
	// ErrNoMember refuses an identity operation of a principal that names
	// no member.
	ErrNoMember = errors.New("the credential names no member")
	// ErrNoIdentity refuses an unlink of a kind the member holds no
	// identity of.
	ErrNoIdentity = errors.New("no identity of that kind")
)

// Link is an organization admin's link of two members, a member_links
// row. The zero time is a sign-in or completion that has not happened.
type Link struct {
	OrganizationID kernel.OrganizationID
	ID             kernel.ULID
	// MemberID is the member that remains; OtherMemberID the member the
	// merge moves into it and deletes.
	MemberID      kernel.MemberID
	OtherMemberID kernel.MemberID
	// CreatedBy is the principal that opened it, as <kind>:<actor ID>.
	CreatedBy           string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	MemberSignedAt      time.Time
	OtherMemberSignedAt time.Time
	CompletedAt         time.Time
}

// Open reports whether l still takes sign-ins at now: it has not
// completed and has not expired.
func (l Link) Open(now time.Time) bool {
	return l.CompletedAt.IsZero() && now.Before(l.ExpiresAt)
}

// Candidate is a member a link names, as the store reads it under the
// organization's lock.
type Candidate struct {
	ID         kernel.MemberID
	Status     member.Status
	Identities int
}

// Linkable refuses a link of a and b that could never complete: one
// member named twice, a deactivated member, or a member without an
// identity to sign in through. Each refusal is a FieldError naming the
// field of the member at fault.
//
// ref: decision 0006 § "Members and identity providers"
func Linkable(a, b Candidate) error {
	if a.ID == b.ID {
		return &organization.FieldError{Field: "otherMemberId", Reason: "names the member itself"}
	}
	for _, c := range []struct {
		field string
		m     Candidate
	}{{"memberId", a}, {"otherMemberId", b}} {
		switch {
		case c.m.Status == member.Deactivated:
			return &organization.FieldError{Field: c.field, Reason: "is a deactivated member"}
		case c.m.Identities == 0:
			return &organization.FieldError{Field: c.field, Reason: "has no identity to sign in through"}
		}
	}
	return nil
}

// Prove records the sign-in of the member m, the member the sign-in's
// identity resolves to, in l at now. browser is the member whose session
// the browser held when it signed in, zero for none. It refuses a link
// that is no longer open (ErrEnded), an identity of neither member
// (ErrNotLinked), and a second sign-in from a browser that does not hold
// the first one's session (ErrOtherBrowser), which keeps the two proofs
// one person's. It reports whether both members have now signed in, when
// the store merges them.
//
// ref: decision 0006 § "Members and identity providers"
func Prove(l Link, m, browser kernel.MemberID, now time.Time) (Link, bool, error) {
	if !l.Open(now) {
		return l, false, fmt.Errorf("%w: link %s", ErrEnded, l.ID)
	}
	mine, other := &l.MemberSignedAt, l.OtherMemberSignedAt
	otherID := l.OtherMemberID
	switch m {
	case l.MemberID:
	case l.OtherMemberID:
		mine, other, otherID = &l.OtherMemberSignedAt, l.MemberSignedAt, l.MemberID
	default:
		return l, false, fmt.Errorf("%w: link %s", ErrNotLinked, l.ID)
	}
	if !other.IsZero() && browser != otherID {
		return l, false, fmt.Errorf("%w: link %s", ErrOtherBrowser, l.ID)
	}
	*mine = now
	done := !other.IsZero()
	if done {
		l.CompletedAt = now
	}
	return l, done, nil
}

// Federation is an identity that signs in as a member, a
// member_identities row with its provider's kind.
type Federation struct {
	ProviderID kernel.ULID
	Kind       sso.Kind
	Subject    string
	// Login is what the identity's first sign-in asserted; empty when the
	// provider asserted none.
	Login     string
	CreatedAt time.Time
}

// Identity is a member's identities, oldest first.
type Identity struct {
	MemberID    kernel.MemberID
	Federations []Federation
}

// PlanUnlink chooses which of all, a member's identities, an unlink of
// kind removes: every one of the kind but own, the identity the caller's
// session signed in through, nil for none. It refuses a kind the member
// holds no identity of (ErrNoIdentity), and as a FieldError naming
// provider, an unlink that removes only own or leaves the member no
// identity to sign in through.
//
// ref: report 14 section 6.5
func PlanUnlink(all []Federation, kind sso.Kind, own *Federation) ([]Federation, error) {
	var ofKind, remove []Federation
	for _, f := range all {
		if f.Kind == kind {
			ofKind = append(ofKind, f)
		}
	}
	if len(ofKind) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoIdentity, kind)
	}
	for _, f := range ofKind {
		if own == nil || f.ProviderID != own.ProviderID || f.Subject != own.Subject {
			remove = append(remove, f)
		}
	}
	switch {
	case len(remove) == 0:
		return nil, &organization.FieldError{
			Field: "provider", Reason: "names only the identity this session signed in through",
		}
	case len(remove) == len(all):
		return nil, &organization.FieldError{
			Field: "provider", Reason: "names every identity of the member, which then could not sign in",
		}
	}
	return remove, nil
}

// Without is all but remove, in all's order.
func Without(all, remove []Federation) []Federation {
	return slices.DeleteFunc(slices.Clone(all), func(f Federation) bool {
		return slices.ContainsFunc(remove, func(r Federation) bool {
			return r.ProviderID == f.ProviderID && r.Subject == f.Subject
		})
	})
}

// UnlinkBody is the trail entry body of an unlink that removed remove.
func UnlinkBody(remove []Federation) json.RawMessage {
	type removed struct {
		Provider         sso.Kind `json:"provider"`
		ProviderID       string   `json:"providerId"`
		ExternalID       string   `json:"externalID"`       //nolint:tagliatelle // the API's IdentityFederation names it so
		ExternalUsername string   `json:"externalUsername"` //nolint:tagliatelle // as above
	}
	out := make([]removed, 0, len(remove))
	for _, f := range remove {
		out = append(out, removed{f.Kind, f.ProviderID.String(), f.Subject, f.Login})
	}
	b, _ := json.Marshal(map[string]any{"removed": out}) //nolint:errchkjson // strings only
	return b
}

// MergeEntry is the trail entry of l's merge, made by the member that
// remains through the sign-in from ip at at.
func MergeEntry(l Link, ip string, at time.Time) kernel.OrganizationAuditEntry {
	b, _ := json.Marshal(map[string]string{ //nolint:errchkjson // strings only
		"linkId": l.ID.String(), "otherMemberId": l.OtherMemberID.String(),
	})
	return kernel.OrganizationAuditEntry{
		OrganizationID:   l.OrganizationID,
		ActorKind:        kernel.PrincipalUser,
		ActorID:          l.MemberID.String(),
		Action:           ActionMerge,
		EventType:        "UPDATE",
		AffectedResource: kernel.AuditResource{Type: ResourceMember, ID: l.MemberID.String()},
		Body:             b,
		RemoteIP:         ip,
		CreatedAt:        at,
	}
}
