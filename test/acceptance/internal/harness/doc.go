// Package harness runs the acceptance suite's scenarios: Features runs the
// Gherkin features with godog, giving every scenario a World that each
// area's steps share, and MainWithBuild builds cmd/infraweave and
// cmd/infraweavectl with coverage once per test binary. A World holds the
// scenario's servers (StartServer, Launch, TwoReplicas), its database and
// object storage, an @objectstore scenario's in a RustFS bucket of its
// own, its named tokens, IDs and pool keys, the answers it was sent
// (Send, Do, SendAs, Last), its browser (Jar) and the settings sections
// every server it starts reads, with the Fakes its tags start merged in.
// Tenants are the accounts seeded through SQL with their API keys;
// Partitions are CI's slices of the features; captures.go loads and
// replays the capture fixtures. Launchers are what a @docker scenario
// runs in Docker: the minimal runner image, the Linux builds of the
// launcher's runner, the runner images of deploy/images (Build), and a
// tool's process (RunTool) in a container of one; RunnerContract holds a
// runner image to deploy/images/contract.sh. A Relay is the worker API and
// state backend as those containers reach them, recording each call and
// holding reports or UNLOCK calls on demand. A failed scenario leaves its
// evidence in out/<scenario>/.
//
// ref: decision 0016
//
// # Input and output
//
// It reads the features and the variables Env* name, builds and runs
// infraweave as subprocesses (serve modes, migrate) whose JSON logs give
// their listeners, builds the runner images from deploy/images with the
// Docker daemon's builder, serves the pinned tool releases to launchers
// as a tool mirror, reaches their listeners over loopback HTTP, queries
// the scenario's database, and writes godog's cucumber report, the
// subprocesses' coverage and a failed scenario's evidence.
//
// # Dependencies
//
// godog runs the features and test/deps starts the containers a
// scenario's tags name. pkg/sdk is every request's client, and
// platform/crypt/kek the Vault KEK settings a rewrap leaves. It imports no
// step package; each area registers itself through the function Features
// is given.
package harness
