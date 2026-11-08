// Package operator is the operator API's organizations.
// Service creates an organization with its first account, unclaimed, and
// optionally its break-glass credential, in one write; lists, reads and
// deletes organizations, a delete taking the organization's accounts and
// everything the access domain holds for them and dropping each revoked
// session from the replica's cache; and sets or removes an organization's
// break-glass credential. Input it refuses is organization.ErrInvalid or
// breakglass.ErrInvalid, naming each field; a slug that cannot be one is
// not found. Each write carries its organization trail entry, by the
// calling operator principal and from its address, and no entry holds a
// password hash.
//
// ref: decision 0021
//
// # Input and output
//
// None of its own: Service reads and writes through Store.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's account, breakglass, organization and port packages. The
// access domain's HTTP adapter, pg adapter and assembly use it.
package operator
