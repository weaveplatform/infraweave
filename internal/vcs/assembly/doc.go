// Package assembly constructs the vcs domain, the source of a stack's
// commits, after the configuration domain. Sources is its implementation
// of vcsapi.Sources over each provider's Source, raw Git's as GIT, and
// StackSources the same resolution as the stacks domain's
// stacksapi.StackSources, which internal/app binds there. Subscribe
// subscribes the Track consumer, which sets a stack's branch head and
// tracked commit whenever a stacksapi.Changed marks its source given or
// changed. API is its partial of the API's strict server, aliased VCSAPI.
// The domain keeps no data of its own, so it registers no migration
// stream.
//
// # Input and output
//
// New takes the stacks domain's api and the vcs settings in Deps; a
// vcs.ca_file it cannot read, or that holds no certificate, fails it with
// ErrConfig.
//
// # Dependencies
//
// It imports internal/kernel, platform/outbox, platform/queue, the stacks
// api package and the domain's api, http, rawgit, port and source
// packages. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
