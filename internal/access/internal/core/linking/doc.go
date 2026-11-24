// Package linking makes one person's identities from two identity
// providers one member, and one seat, only when an organization admin
// links the two members and the person proves both. Nothing is ever linked
// by e-mail address, since a provider may let anyone claim any address.
//
// Service.Create opens a Link for an organization admin, admitted through
// sso.Admins: the member that remains and the other member, which must be
// two members of the caller's organization (port.ErrNotFound otherwise)
// that Linkable admits, neither deactivated and each holding an identity
// (a FieldError otherwise), with no open link of the pair
// (ErrLinkExists). A link is open for TTL, an hour. Its page, at URL, is
// Page: the enabled OIDC providers each member holds an identity at, until
// that member has signed in. Start starts a sign-in through one of them
// with the purpose link, which asks the provider for a fresh
// authentication (max_age=0, prompt=login) and returns the browser to the
// page.
//
// Prove is the rule the store applies to each link sign-in under the
// organization's lock: the identity must be one of the two members'
// (ErrNotLinked), the link open (ErrEnded), and the second sign-in must
// come from the browser that holds the first one's session
// (ErrOtherBrowser). The second sign-in merges the other member into the
// member: identities, account users, role bindings of actor kind user,
// personal keys and the organization role move, the member's own row
// winning where both hold one; the other member's sessions are revoked on
// every replica and its row is deleted, with MergeEntry on the trail.
//
// Unlink removes the caller's member's identities of one provider kind,
// as PlanUnlink chooses: never the identity the caller's session signed in
// through, nor the member's last; a kind it holds none of is
// ErrNoIdentity, and a principal that names no member ErrNoMember. The
// sessions signed in through a removed identity are revoked. Identity is
// a member's identities for the viewer.
//
// ref: decisions 0006 and 0021
//
// # Input and output
//
// None of its own: Service reads and writes through Store, starts
// sign-ins through SignIn, and admits organization admins through
// sso.Admins. kernel.NewULID reads the clock and crypto/rand.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's member, orgadmin, organization, port and sso packages. The
// access domain's HTTP adapter, pg adapter and assembly use it.
package linking
