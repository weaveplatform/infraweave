// Package role holds the roles and role bindings. Seed is the
// five system roles every account starts with: space-reader, space-writer
// and space-admin, defined from the catalogue's fallback column, and
// worker-pool-controller and drift-detection-manager. SystemActions is a
// system role's actions, computed from accessapi's catalogue and never
// stored, and leaving out every disabled or deprecated action. Service
// keeps each account's custom roles beside them: a custom role's slug is
// SlugOf its name, kept through every rename, and it holds only actions
// the catalogue lists and neither disables nor deprecates, refused at
// their pointers otherwise. A system role refuses every change as
// ErrSystem, and a role with bindings its delete as ErrInUse. Binding is
// one grant of a role to an actor (a user, group, API key, stack or Flows
// project) in a space, made as a role binding or from a legacy access
// rule; the binding operations arrive with the rest of P4b.
//
// ref: decision 0007 § "Roles and the action catalogue"
//
// # Input and output
//
// None: the access domain's pg adapter stores roles and bindings, and its
// http adapter serves the Roles tag through Service.
//
// # Dependencies
//
// It imports accessapi for the action catalogue, internal/kernel and the
// standard library. The access domain's adapters and assembly import it.
package role
