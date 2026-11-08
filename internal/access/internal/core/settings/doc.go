// Package settings holds the account-wide settings no other domain owns:
// whether members may create personal API keys and may ask for them, whether
// API keys may manage API keys, the security contact, the OAuth redirect
// allowlist, the awareness survey and the deletion mark that ends in
// deleting the account. Service reads and sets them for
// the caller's account through Store. CheckRedirectHosts and
// CheckSecurityEmail refuse a value with ErrInvalid naming it; only a
// person flips NonHumanKeyManagers, a key or a run being ErrNotHuman; and
// an account is deleted only while marked and not its organization's last,
// ErrInvalidState otherwise. No waiting period follows the mark.
//
// ref: decisions 0006 and 0007
//
// # Input and output
//
// None of its own: Store reads and writes, and records the request for
// personal keys and the deletion on the audit trails. Service drops each
// session a change revoked from the replica's cache through Drop.
//
// # Dependencies
//
// It imports internal/kernel and the standard library. The access domain's
// adapters and assembly import it.
package settings
