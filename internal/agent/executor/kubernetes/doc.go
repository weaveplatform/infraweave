// Package kubernetes is the Kubernetes executor.Executor, which executes
// each run in a Pod of the run's image in the worker's namespace, owned
// by the worker's resource.
//
// Start packs the job's workspace into a staging file and creates a Pod
// named for the run and its state version, so a confirmed run's apply
// gets a new Pod, with a fixed shape no pool setting changes: no service
// account token, no service links, no host namespace or path, UID and GID
// 1983, every capability dropped, no privilege escalation, RuntimeDefault
// seccomp, restartPolicy Never and an active deadline of the time left
// to the job's preparing deadline, or the configured preparation bound
// when it has none, plus the configured execution bound, the run timeout
// and cleanup after it. Its own volumes
// are Pod-local emptyDirs; beside them the runner mounts each of the
// job's Secret or ConfigMap keys as a read-only file and claims as
// read-only directories, and at each of its scoped directories the
// directory the bootstrap creates, for the run's user, in an in-memory
// emptyDir of its own, which its runner fills and leaves unwritable. The Pod is labelled with the
// worker, the run, the vendor and the tool, and Runtime names the
// executor kubernetes. Its bootstrap init container, of the
// same-version infraweave image, is attached once it runs and receives
// the bootstrap package's stream of the workspace and the job's verified
// tools; once it exits 0, the runner container starts the copied
// infraweave executable as runner, with each tool mounted read-only over
// /bin/<tool>, and is attached in turn: its standard input takes the
// handoff and closes, and its output goes to the job's writer. Secrets
// reach the Pod only over those streams, never its specification,
// annotations, status or logs. An image that cannot be pulled, a
// container that cannot start, a refused stream or a preparation that
// outlasts the job's preparing deadline, or the configured bound when it
// has none, deletes the Pod and fails Start; a deadline already passed
// creates no Pod. A job that mounts
// any host path but its verified tools is refused, and a Pod that
// streams tools selects nodes of their architecture.
//
// Wait reads the Pod until its runner ends: its exit code, killed after
// Kill, out-of-memory when the kubelet ended it so, run-timeout when the
// active deadline ended the Pod, evicted when the cluster evicted or
// preempted it, which the Pod's Evicted reason or DisruptionTarget
// condition shows, even once it is gone, and missing when the Pod is
// otherwise gone or ended first. Stop runs the fixed infraweave runner signal interrupt
// beside the runner, which sends its PID 1 SIGINT; Kill deletes the Pod
// with a one-second grace; Remove deletes it. Find answers a worker's
// newest Pod after a restart, following its runner's output from then on,
// and deletes its older ones; it considers only Pods the configured owner,
// the worker's resource, controls, so it never adopts or deletes a Pod of
// another worker, pool, namespace or run. A Pod never handed its handoff
// cannot be resumed and is deleted; one that was is observed as
// acknowledged.
//
// Command runs an initialization command's helper in a Pod of its own,
// labelled for its worker and run with labels no run Pod carries: the
// same fixed security, owner, pool resources, placement and pull
// secrets, an active deadline of the call's remaining time, a bootstrap
// that receives only the executable and an empty workspace, and a helper
// container of the run's image that mounts the executable read-only and
// nothing else and starts it as runner command. The request reaches the
// helper only through its attached standard input, its standard output
// goes to the caller, and the Pod is deleted however the call ends; a
// helper image that cannot be pulled is executor.ErrImage. Find first
// deletes any helper Pod of its worker a stopped controller left.
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
// k8s.io/api and apimachinery, internal/agent/bootstrap, executor,
// phaseinputs' size bound and wire. internal/agent/controller runs one per slot.
package kubernetes
