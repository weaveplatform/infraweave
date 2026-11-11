// Package assembly constructs the vcs domain, the source of a stack's
// commits, after the configuration domain. Sources is its implementation
// of vcsapi.Sources over each provider's Source, raw Git's as GIT and
// GitHub's as GITHUB and GITHUB_ENTERPRISE, GitLab's as GITLAB, and
// StackSources the same resolution as the stacks domain's
// stacksapi.StackSources, which internal/app binds there. Subscribe
// subscribes the Track consumer, which sets a stack's branch head and
// tracked commit whenever a stacksapi.Changed marks its source given or
// changed. The domain keeps each account's VCS integrations of
// Providers: Stream is its migration stream, Held the columns that name
// other domains' rows and SealedColumns its sealed secrets; SpaceGuard
// and SpaceOf, for Entity, are its say in the access domain;
// StackIntegrations the check of the integration a stack's write names,
// which internal/app binds in the stacks domain; and Purge deletes a
// deleted account's integrations. API is its partial of the API's strict
// server, aliased VCSAPI.
//
// # Input and output
//
// New takes the database, the keyring, the access domain's spaces and
// authorizer, the stacks domain's api, the vcs settings and the server's
// public origin in Deps; a vcs.ca_file it cannot read, or that holds no
// certificate, and a vcs.github_app whose private key or webhook secret
// file it cannot read, fail it with ErrConfig. vcs.github_app's App is
// the GITHUB provider; with no app_id, that provider answers 503.
//
// # Dependencies
//
// It imports the access and stacks api packages, internal/kernel,
// platform/crypt, platform/outbox, platform/queue, platform/sql's
// integrity and migrate packages, platform/tx and the domain's api, http,
// github, gitlab, pg, rawgit, integration, port and source packages. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
