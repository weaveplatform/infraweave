// Package member is the member: a person in an organization,
// which accounts admit. Status is Active once the person has signed in,
// Pending from an invitation until they sign in through it, and
// Deactivated once an organization admin deactivates them; an active or
// pending member holds one seat however many accounts admit it.
// CheckEmail checks an address input names.
//
// ref: decision 0006 § "Members and identity providers"
//
// # Input and output
//
// None; it declares types and checks values.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's organization package. The access domain's orgadmin, user and
// group packages and its pg and http adapters use it.
package member
