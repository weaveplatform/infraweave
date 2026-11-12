// Package account holds the account: the data boundary every
// account-scoped row carries in account_id, with an immutable slug unique
// across the installation, a display name, and the claim that names its
// first account admin.
//
// ref: decision 0007 § "Tenancy"
//
// # Input and output
//
// None. New reads the clock and crypto/rand through kernel.NewAccountID.
//
// # Dependencies
//
// It imports core/organization for the slug and name rules, internal/kernel
// and the standard library. The access domain's adapters and assembly
// import it.
package account
