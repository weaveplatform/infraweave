// Package vcs holds the acceptance steps of features/vcs: sources and the
// VCS integrations of each provider. Each provider's steps are a
// harness.Fake that starts its stand-in when the scenario's tags name it:
// Gitea from test/deps for @vcs, serving HTTPS under the run's CA, and
// in-process fakes for @github, @gitlab, @bitbucket-cloud, @bitbucket-dc
// and @azure-devops, whose certificates the servers trust through
// vcs.ca_file. The @vcs-lab scenarios reach the real providers the
// scheduled vcs-lab job provisions instead.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk, creates
// repositories and commits on Gitea and the fakes, and seeds and reads the
// servers' database through SQL. The lab steps read the provider and
// repositories the vcs-lab job names in vcslab's variables.
//
// # Dependencies
//
// It builds on harness.World and test/deps. The fakes are vcs/githubfake,
// vcs/gitlabfake, vcs/bitbucketcloudfake, vcs/bitbucketdcfake and
// vcs/azuredevopsfake, and vcs/vcslab reads the lab's variables.
package vcs
