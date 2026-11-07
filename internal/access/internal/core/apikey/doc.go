// Package apikey is decision 0006's API keys. A secret is Prefix, iwk_,
// and 43 base64url characters encoding 32 random bytes (NewSecret); only
// its SHA-256 is stored (Hash), and Matches compares digests in constant
// time. Usable is whether a key may exchange: neither disabled, deleted
// nor expired. An OIDC key has no secret: it exchanges a JWT of its
// issuer whose sub matches CompileSubject's anchored expression, and
// OIDC.Teams adds the values of the claim its mapping names TeamsClaim
// to the key's teams. An organization key (decision 0007) is an
// organization's, not an account's: its secret is OrganizationPrefix,
// iwo_, and 43 base64url characters (NewOrganizationSecret), stored as
// HashOrganization's SHA-256, and OrganizationKey.Usable and Matches
// mirror an API key's.
//
// Service manages the keys of the caller's account through Store: create,
// list, read, update, reset, disable and delete, where reset, disable and
// delete revoke every session of the key and drop them from the replica's
// cache. Input it refuses is ErrInvalid, naming the field; IssuerAllowed
// is DV-014's issuer rule. A personal key has an Owner, the member whose
// principal in the key's account its tokens act as: CreatePersonal takes
// it from a member's own session, any other caller being ErrNoOwner, and
// the store refuses it with ErrPersonalOff while the account turns
// personal keys off; Personal, PersonalCount and DisablePersonal list,
// count and disable the account's personal keys. A personal key whose
// owner leaves the account or is deactivated is disabled as
// DEPROVISIONED.
//
// # Input and output
//
// None of its own: Service reads and writes through Store. NewSecret reads
// crypto/rand.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The access
// domain's authn service, HTTP adapter, pg adapter and assembly use it.
package apikey
