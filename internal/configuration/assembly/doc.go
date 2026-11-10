// Package assembly constructs the configuration domain, the contexts and
// config elements that stacks read, after stacks. API is its partial of
// the API's strict server, aliased ConfigurationAPI; Stream is its
// migration stream, which internal/app registers after stacks'; Held and
// SealedColumns are its columns for the orphan count and the key
// rotation; SpaceGuard and StackGuard are its say in space and stack
// deletes and moves; Configuration is the stack view's reader; SpaceOf and
// AttachmentSpaceOf find the space the access domain decides a context's
// and an attachment's operations on; Subscribe subscribes Autoattach,
// which follows each stack change with the stack's automatic attachments;
// Surfaces declares the environment expression surface, BindExpressions
// binds the registry internal/app builds from it, and Environment
// resolves a stack's environment for a run; Purge deletes a deleted
// account's contexts, config elements and attachments PurgeBatch rows at
// a time, which internal/app runs for each accessapi.Deleted.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's api, core, http and pg packages, the access and
// stacks api packages, internal/kernel, platform/crypt, platform/expr,
// platform/outbox,
// platform/queue and platform/sql/integrity and migrate. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
