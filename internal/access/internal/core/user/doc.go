// Package user is Spacelift's managed users as infraweave serves them: a
// managed user is a member of the organization as one account sees it
// (decision 0007). Invite admits the organization's member with the
// invited login to the caller's account, creating a pending member when
// there is none, and records an Invitation with its e-mail address and
// sentAt; no e-mail is sent (capture C251), and the person accepts by
// signing in through the invitation's link, never by an e-mail match
// (decision 0006), which P23's sign-in serves. Inviting a member the
// account admits, a deactivated member, or an e-mail address with a
// pending invitation in the account is ErrInvalidState, and the
// invitation's seat is checked through the features domain unless
// another account already admits the member. State derives EXPIRED from
// an invitation's sentAt after InvitationTTL (C125); Resend restarts it.
// Delete stops the account admitting the member: their sessions in it
// are revoked and their personal keys in it disabled as DEPROVISIONED,
// and they stay a member of the organization. Role bindings and space
// access rules arrive with P4b, so an input carrying any is
// ErrBindingsUnsupported rather than dropped. RequestInvite and
// RequestRoleChange record the request on the account's trail; only a
// member asks for a role, on the root space until P4's spaces.
//
// # Input and output
//
// None of its own: Service reads and writes through Store.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's apikey, member, organization and port packages. The group
// package, the pg and http adapters and the assembly use it.
package user
