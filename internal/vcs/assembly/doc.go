// Package assembly constructs the vcs domain, the source of a stack's
// commits, after the configuration domain. Sources is its implementation
// of vcsapi.Sources over each provider's Source, raw Git's as GIT,
// GitHub's as GITHUB and GITHUB_ENTERPRISE, GitLab's as GITLAB,
// Bitbucket Cloud's as BITBUCKET_CLOUD, Bitbucket Data Center's as
// BITBUCKET_DATACENTER and Azure DevOps's as AZURE_DEVOPS, and
// StackSources the same resolution as the
// stacks domain's stacksapi.StackSources, which internal/app binds there. Subscribe
// subscribes the Track consumer, which sets a stack's branch head and
// tracked commit whenever a stacksapi.Changed marks its source given or
// changed. The domain keeps each account's VCS integrations of
// Providers: Stream is its migration stream, Held the columns that name
// other domains' rows and SealedColumns its sealed secrets; SpaceGuard
// and SpaceOf, for Entity, are its say in the access domain;
// StackIntegrations the check of the integration a stack's write names,
// which internal/app binds in the stacks domain; and Purge deletes a
// deleted account's integrations. Jobs are its job handlers, which the
// scheduler registers: Revoke's, which revokes a run's minted tokens once
// vcsapi.Sources.Release enqueues it. API is its partial of the API's
// strict server, aliased VCSAPI.
// Routes mounts provider webhook intake outside that JSON API. The intake
// commits each verified delivery and its processing job together, with large
// bodies in inbox; Jobs also registers its feature-gated processor, and Cron
// prunes retained delivery rows hourly. Purge deletes integrations to fence new
// intake, then drops deliveries and schedules their bodies for deletion.
// A minute reconciliation task settles deliveries whose processing jobs died or
// were discarded, using bounded pages and durable cursor continuations.
//
// # Input and output
//
// New takes the database, the keyring, the access domain's spaces and
// authorizer, the features domain's option policy, the stacks domain's api,
// the vcs settings, the server's
// public origin and the log a run's fallback credential is noted in, in
// Deps; a vcs.ca_file it cannot read, or that holds no
// certificate fail it with ErrConfig. Accounts register their own GitHub
// Apps; GITHUB aliases their GITHUB_ENTERPRISE integrations.
// vcs.bitbucket_cloud.api_host is where BITBUCKET_CLOUD integrations
// reach Bitbucket Cloud.
//
// # Dependencies
//
// It imports the access, features and stacks api packages, internal/kernel,
// platform/crypt, platform/outbox, platform/queue, platform/sql's
// integrity and migrate packages, platform/tx and the domain's api, http,
// azuredevops, bitbucketcloud, bitbucketdc, github, gitlab, pg, rawgit,
// integration, port and source packages. Only internal/app and cmd/ import it, as the
// constructors rule enforces.
package assembly
