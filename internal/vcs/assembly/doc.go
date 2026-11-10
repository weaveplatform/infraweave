// Package assembly constructs the vcs domain, the source of a stack's
// commits, after the configuration domain. Sources is its implementation
// of vcsapi.Sources over each provider's Source, raw Git's as GIT; API is
// its partial of the
// API's strict server, aliased VCSAPI. The domain keeps no data yet, so
// it registers no migration stream.
//
// # Input and output
//
// New takes the stacks domain's api in Deps.
//
// # Dependencies
//
// It imports the stacks api package and the domain's api, http, rawgit,
// port and source packages. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
