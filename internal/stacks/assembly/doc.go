// Package assembly constructs the stacks domain over its Deps. Stacks is
// the stacksapi.Stacks that the domains above it call; SpaceGuard is the
// accessapi.SpaceGuard internal/app registers with the access domain, so
// a space a stack lives in cannot be deleted; API is its partial of the
// API's strict server, aliased StacksAPI; Stream is its migration stream,
// which internal/app registers after access'.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL through Deps.
//
// # Dependencies
//
// It imports accessapi, stacksapi, the domain's stacks, http and pg
// packages, platform/sql/migrate and platform/tx. Only internal/app and
// cmd/ import it, as the constructors rule enforces.
package assembly
