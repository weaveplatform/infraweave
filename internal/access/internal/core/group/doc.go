// Package group is Spacelift's managed user groups, each one account's: a
// group names an identity provider group, and its members are the
// account's managed users whose latest sign-in to the account reported
// that name, so a group reaches no other account. Service lists, creates,
// reads, describes and deletes the caller's account's groups, lists a
// group's members, and lists the names the account's latest sign-ins
// reported. A name another group of the account holds is
// port.ErrSlugTaken; the binding service writes the role bindings a
// creation carries, in its transaction, and the store deletes a group's
// bindings with it. Every write carries its entry on the
// account's trail. A Group's Version is raised by every change; Update
// and Delete carry a kernel.Expect, and the store refuses a group whose
// version it does not accept with kernel.ErrStale, changing nothing.
//
// ref: decision 0022
//
// # Input and output
//
// None of its own: Service reads and writes through Store.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's organization, port and user packages. The pg and http adapters
// and the assembly use it.
package group
