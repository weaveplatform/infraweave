// Package orgadmin is the Organization tag (decision 0021 § "The
// organization API"): what an organization's admins do across its
// accounts without reaching any account's stacks, runs, state or secrets.
// Service reads and renames the organization; lists, creates, claims,
// renames and deletes its accounts; lists and deactivates its members and
// grants or revokes their organization role; lists, creates, disables and
// deletes its organization keys; and reads the pooled limits, each
// account's use and limits, and sets or clears an account limit through
// the features domain.
//
// The authenticator admits organization tokens, the break-glass admin and
// members; the service refuses a member without the organization role
// with ErrForbidden. Creating an account claims it, and claiming admits
// the member that becomes its first account admin, which must be an
// active member (ErrNoAdmin, answered as a FieldError naming admin). A
// claimed account is claimed for good, so a second claim, like deleting
// the organization's last account, is ErrInvalidState. Every write
// carries its organization trail entry, and a revoked session is dropped
// from the replica's cache at once.
//
// # Input and output
//
// None of its own: Service reads and writes through Store and the features
// domain's featuresapi.Entitlements. CreateKey reads crypto/rand through
// apikey.NewOrganizationSecret.
//
// # Dependencies
//
// It imports the standard library, internal/kernel, internal/features/api
// and the access domain's account, apikey, organization and port packages.
// The access domain's HTTP adapter, pg adapter and assembly use it.
package orgadmin
