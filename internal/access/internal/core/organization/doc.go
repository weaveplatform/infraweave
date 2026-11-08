// Package organization holds the tenancy rules above the account: an
// organization, its slug and display name. ParseSlug and
// CheckName are the rules accounts share, since an account slug names the
// same external things an organization slug does and is unique across the
// installation.
//
// ref: decision 0007 § "Tenancy"
//
// # Input and output
//
// None. New reads the clock and crypto/rand through kernel.NewOrganizationID.
//
// # Dependencies
//
// It imports internal/kernel and the standard library. The access domain's
// other core packages, its adapters and its assembly import it; nothing
// outside the domain can.
package organization
