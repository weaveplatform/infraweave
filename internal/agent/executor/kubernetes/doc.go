// Package kubernetes is the Kubernetes executor.Executor, which executes
// each run in a Pod of the run's image in the worker's namespace, owned
// by the worker's resource.
//
// Start packs the job's workspace into a staging file and creates a Pod
// named for the run and its state version, so a confirmed run's apply
// gets a new Pod, with a fixed shape no pool setting changes: no service
// account token, no service links, no host namespace or path, UID and GID
// 1983, every capability dropped, no privilege escalation, RuntimeDefault
// seccomp, restartPolicy Never and the pool's active deadline. Its volumes
// are Pod-local emptyDirs. Its bootstrap init container, of the
// same-version infraweave image, is attached once it runs and receives
// the bootstrap package's stream of the workspace and the job's verified
// tools; once it exits 0, the runner container starts the copied
// infraweave executable as runner, with each tool mounted read-only over
// /bin/<tool>, and is attached in turn: its standard input takes the
// handoff and closes, and its output goes to the job's writer. Secrets
// reach the Pod only over those streams, never its specification,
// annotations, status or logs. An image that cannot be pulled, a
// container that cannot start, a refused stream or a preparation that
// outlasts its bound deletes the Pod and fails Start. A job that mounts
// any host path but its verified tools is refused, and a Pod that
// streams tools selects nodes of their architecture.
//
// Wait reads the Pod until its runner ends: its exit code, killed after
// Kill, out-of-memory when the kubelet ended it so, run-timeout when the
// active deadline ended the Pod, and missing when the Pod is gone or
// ended first. Stop runs the fixed infraweave runner signal interrupt
// beside the runner, which sends its PID 1 SIGINT; Kill deletes the Pod
// with a one-second grace; Remove deletes it. Find answers a worker's
// newest Pod after a restart, following its runner's output from then on;
// a Pod never handed its handoff cannot be resumed and is deleted.
//
// ref: docs/implementation/worker-pool-contracts.md § "Portable preparation, handoff and recovery"
//
// # Input and output
//
// It creates, reads, patches and deletes Pods and opens their attach,
// exec and log streams through the Kubernetes API; it reads the job's
// workspace and tool files and writes a staging directory. Observe, when
// configured, is told each Pod's phase. Nothing is logged.
//
// # Dependencies
//
// It imports client-go's typed core client, rest and remotecommand,
// k8s.io/api and apimachinery, internal/agent/bootstrap, executor and
// wire. internal/agent/controller runs one per slot.
package kubernetes
