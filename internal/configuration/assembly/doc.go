// Package assembly constructs the configuration domain, the contexts and
// config elements that stacks read, after stacks. API is its partial of
// the API's strict server, aliased ConfigurationAPI; Stream is its
// migration stream, which internal/app registers after stacks'; Held and
// SealedColumns are its columns for the orphan count and the key
// rotation; SpaceGuard and StackGuard are its say in space and stack
// deletes and moves; Configuration is the stack view's reader; SpaceOf and
// AttachmentSpaceOf find the space the access domain decides a context's
// and an attachment's operations on.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's api, core, http and pg packages, the access and
// stacks api packages, platform/crypt, platform/outbox and
// platform/sql/integrity and migrate. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
