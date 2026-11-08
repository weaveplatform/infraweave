// Package role holds the roles and role bindings. Seed is the
// five system roles every account starts with: space-reader, space-writer
// and space-admin, defined from the catalogue's fallback column, and
// worker-pool-controller and drift-detection-manager. SystemActions is a
// system role's actions, computed from accessapi's catalogue and never
// stored, and leaving out every disabled or deprecated action. Binding is
// one grant of a role to an actor (a user, group, API key, stack or Flows
// project) in a space, made as a role binding or from a legacy access
// rule. Custom roles and the binding operations arrive with P4b.
//
// ref: decision 0007 § "Roles and the action catalogue"
//
// # Input and output
//
// None: the access domain's pg adapter stores roles and bindings.
//
// # Dependencies
//
// It imports accessapi for the action catalogue, internal/kernel and the
// standard library. The access domain's adapters and assembly import it.
package role
