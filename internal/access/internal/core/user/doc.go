// Package user is Spacelift's managed users as infraweave serves them: a
// managed user is a member of the organization as one account sees it.
// Invite admits the organization's member with the
// invited login to the caller's account, creating a pending member when
// there is none, and records an Invitation with its e-mail address and
// sentAt; no e-mail is sent (capture C251). Acceptance belongs to the
// organization sign-in flow and uses the invitation link, never an e-mail
// match. Inviting a member the
// account admits, a deactivated member, or an e-mail address with a
// pending invitation in the account is ErrInvalidState, and the
// invitation's seat is checked through the features domain unless
// another account already admits the member. State derives EXPIRED from
// an invitation's sentAt after InvitationTTL (C125); Resend restarts it.
// Delete stops the account admitting the member: their sessions in it
// are revoked and their personal keys in it disabled as DEPROVISIONED,
// and they stay a member of the organization; the store deletes their
// bindings in the account with the admission. The binding service writes
// the role bindings and space access rules an input carries, in the
// transaction of the write that carries them. RequestInvite and
// RequestRoleChange record the request on the account's trail; only a
// member asks for a role on a space. A User's
// Version is raised by every change of the account's admission of the
// member; Update and Delete carry a kernel.Expect, and the store refuses
// an admission whose version it does not accept with kernel.ErrStale,
// changing nothing. LastSignInKind reads the kind of the identity
// provider a member's session's member last signed in through, for the
// viewer, and nothing for any other principal.
//
// ref: decisions 0006, 0007 and 0022
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
