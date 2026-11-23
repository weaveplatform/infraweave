// Package controller is the Kubernetes worker controller: it reconciles
// the WorkerPool and Worker resources of its watched namespaces with the
// infraweave installation each pool's endpoint names.
//
// One replica of a watch scope leads its Lease and reconciles; the others
// stand by. Each pass lists every WorkerPool and brings it to its
// specification. A Managed pool exchanges the configured management API
// key, checks that it belongs to spec.account, and finds its remote pool
// by its owner (cluster ID, namespace and resource UID), creating it once
// with a certificate signing request of the credential Secret's private
// key under an idempotency key derived from the resource UID; it writes
// only the token key back to that Secret. An Existing or Public pool
// reads its token and private key from the Secret and must name the pool
// its token names. Every pool then binds the remote pool to its owner
// with a signed proof, so a pool bound elsewhere, or managed by another
// resource, is refused rather than taken over.
//
// A pool's capacity is a set of Worker resources, one per slot, named
// from the pool's UID and owned by it. Each slot registers a worker with
// the owner and slot in its signed registration, which resumes the slot's
// worker, and then runs a launcher with the pool's credentials that
// registers the same way and polls the pool's endpoint for runs. The
// launcher executes each run it claims through a Kubernetes executor in a
// Pod owned by the slot's Worker, built from the pool's pod settings,
// with its preparation bounded by the pool's preparing timeout and the
// Pod's active deadline by that timeout, the run timeout and
// cleanup_timeout. Tool binaries are fetched once into cache_dir, from
// tool_mirror when set, and each slot stages its workspaces in its own
// directory under it. The Worker's status shows the slot's worker and the
// assignment it executes: the run, its state and version, its Pod, how far
// its preparation came, its deadlines and its exit; the pool counts its
// busy and ready slots, and ExecutorAvailable is True. A controller with
// no Kubernetes API configuration to stream through runs no launcher:
// ExecutorAvailable is False and its slots, which register each heartbeat
// instead, claim no runs. A slot beyond the organization's private-workers
// limit stays unregistered with the reason LimitReached, and a pool
// creates no further slots while one is refused. Slot launchers run while
// the replica leads; losing the Lease, or a pool removed without its
// finalizer, ends them, and a busy slot's Pod is left for its next
// launcher to resume.
//
// A slot beyond the capacity, and every slot of a deleted pool, is
// retired: its Worker is deleted; its launcher, once its run ends and
// claiming none after it, is ended; its worker deregistered and then its
// finalizer released. A deleted Managed pool's remote pool is deleted
// through the ordinary delete and its guards; an Existing or Public pool's
// binding is released. Only then is the pool's finalizer released. Each
// cleanup attempt is bounded by cleanup_timeout; a refusal is recorded in
// the Terminating condition and the next pass retries it.
//
// ref: docs/implementation/worker-controller-design.md
//
// # Input and output
//
// It reads the worker_controller configuration, WorkerPool, Worker and
// Secret resources and the Lease through the Kubernetes API, writes
// finalizers and status with patches under strict field validation,
// creates and deletes run Pods and streams into them, and calls the
// worker API and the account API of each pool's endpoint over HTTPS
// trusting the system roots and ca_file. It never writes a private key,
// management credential, session token or opened secret to a resource or
// a log.
//
// # Dependencies
//
// It imports client-go's dynamic, core and coordination clients and
// leader election, internal/agent/client, config, executor/kubernetes,
// initpolicy, launcher, tools and the generated wire/management client,
// kernel, and platform/observe and rego; no server package.
package controller
