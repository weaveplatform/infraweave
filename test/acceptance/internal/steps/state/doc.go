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
// wrote, from the database. The unit-only check omits the stack's unwritten
// default workspace when the project root holds no unit, while retaining
// unexpected named workspaces and any written default. Force-unlock scenarios
// seed the locks a stopped server could leave, inspect their complete audit metadata and broken-lock
// records, and arrange the proposed holder as a writing caller so its next
// write reaches the broken-lock check. A
// scenario can instead hand a run it took over to
// this build's runner, started as a launcher starts it in the tool's
// runner image, which executes the run's phases and reports them itself.
// For a run a real launcher executes, the relay steps act on the relay
// the launcher reaches the servers through, and find the backend's path
// by the run's stack; a step may name a workspace of the stack other
// than its default one, such as a Terragrunt unit's.
// The steps of the signed state URLs keep the URL an answer names and
// send it, with no credential, to either replica's listener in place of
// server.public_url; they move time on for a URL by signing its token
// again with its expiry moved back, under the key the scenario's keyring
// derives for the token's kind, and for an upload by moving its creation
// and expiry back, which blob.expire then reads. The steps of external access send the backend's
// requests with a tenant's API key, a kept token or a run token as HTTP
// Basic credentials, write a terraform_remote_state of another stack into
// a run's workspace or a user's workspace of its own, whose credential
// reaches the tool only in its environment, and check that no Terraform
// Cloud API is discovered. The import steps put the state a run's tool
// pulled at an upload URL, keep the object ID the URL's answer names for
// a stack's creation or import to name, and check the imported version a
// stack's default workspace holds, the upload the import consumed and the
// import the audit trail records. The rollback steps search a stack's
// state versions, keep one's ID for a rollback to name, and check the
// version a rollback answers and makes current, resealed apart from its
// target, and the rollback the audit trail records. The retention steps
// move time on for a stack's versions by moving their creation back, and
// check which versions state.retention deleted, row, record and object,
// and which it kept.
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
// scenario's directory; the import steps read the versions, uploads and
// audit trail entries an import or a rollback writes through SQL, the
// retention steps age a stack's versions there, and the URL steps an
// upload's blob_objects row, and read the scenario's keyring to sign a
// URL's token again. A failed scenario
// leaves the relay's calls and each tool's output beside the servers'
// evidence. It sends a signed URL's request and a backend request of its
// own with net/http, recording its answer for the answer steps.
//
// # Dependencies
//
// It builds on harness.World, its Launchers and Relay, test/deps's
// pinned tools, Gitea, object store and Eventually, internal/agent's
// backend, handoff and wire packages, platform/seal, and platform/crypt's
// keyring, through harness.World.Keyring.
package state
