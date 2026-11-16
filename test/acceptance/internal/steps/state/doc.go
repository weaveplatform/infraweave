// Package state holds the acceptance steps of features/state: Terraform,
// OpenTofu and Terragrunt's units keeping a run's state in infraweave's
// managed state backend. The scenario is the run's worker: it takes over a run a poll
// assigned, opening its sealed specification with the pool's key,
// checking out its commit from Gitea and writing the backend file and
// credentials infraweave's agent writes; it reports the run's phases over
// the worker API, recording the apply's outputs before its report as a
// runner does; and it runs the pinned terraform or tofu in containers
// of harness.RunnerImage, which reach the server through the scenario's
// relay. The relay records each call and each 423's body, and holds the
// backend's UNLOCK calls on demand, so a lock stays held while another
// process asks for it, or its writes, so a tool holds its lock with its
// write still to come. The scenario reads the stack's Tofu workspaces,
// whose locks those are, through the API, as a search's, a release's or
// one workspace's answer, and the releases the home account's audit
// trail records with the caller's address, and the state workspaces of
// a run's stack, one per Terragrunt unit, each at a version the run
// wrote, from the database. A
// scenario can instead hand a run it took over to
// this build's runner, started as a launcher starts it in the tool's
// runner image, which executes the run's phases and reports them itself.
// The steps of the signed state URLs keep the URL an answer names and
// send it, with no credential, to either replica's listener in place of
// server.public_url. The steps of external access send the backend's
// requests with a tenant's API key, a kept token or a run token as HTTP
// Basic credentials, write a terraform_remote_state of another stack into
// a run's workspace or a user's workspace of its own, whose credential
// reaches the tool only in its environment, and check that no Terraform
// Cloud API is discovered.
//
// ref: decision 0008
// ref: decision 0010
// ref: decision 0016
//
// # Input and output
//
// It sends the worker API's requests to the scenario's servers through
// pkg/sdk, starts and removes containers through the suite's Docker
// daemon, reads the scenario's database through SQL and its bucket in
// RustFS through S3, and writes each run's workspace under the
// scenario's directory. A failed scenario leaves the relay's calls and
// each tool's output beside the servers' evidence. It sends a signed
// URL's request and a backend request of its own with net/http,
// recording its answer for the answer steps.
//
// # Dependencies
//
// It builds on harness.World, its Launchers and Relay, test/deps's
// pinned tools, Gitea, object store and Eventually, internal/agent's
// backend, handoff and wire packages, and platform/seal.
package state
