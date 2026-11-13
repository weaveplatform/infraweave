// Package state holds the acceptance steps of features/state: Terraform
// and OpenTofu keeping a run's state in infraweave's managed state
// backend. The scenario is the run's worker: it takes over a run a poll
// assigned, opening its sealed specification with the pool's key,
// checking out its commit from Gitea and writing the backend file and
// credentials infraweave's agent writes; it reports the run's phases over
// the worker API; and it runs the pinned terraform or tofu in containers
// of harness.RunnerImage, which reach the server through the scenario's
// relay. The relay records each call and each 423's body, and holds the
// backend's UNLOCK calls on demand, so a lock stays held while another
// process asks for it. A scenario can instead hand a run it took over to
// this build's runner, started as a launcher starts it in the tool's
// runner image, which executes the run's phases and reports them itself.
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
// each tool's output beside the servers' evidence.
//
// # Dependencies
//
// It builds on harness.World, its Launchers and Relay, test/deps's
// pinned tools, Gitea, object store and Eventually, internal/agent's
// backend, handoff and wire packages, and platform/seal.
package state
