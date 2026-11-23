package executor

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Job is one run's container: the worker and the run it belongs to, which
// label it so a restarted launcher finds it, the run's state version
// after its claim, which names the assignment, the image it starts from,
// the host directory that is its workspace, the image's own when empty,
// the other host paths it mounts, the verified tool executables among
// them, the cluster objects it mounts, the directories it provides empty
// for the runner's scoped files, the handoff its runner reads on standard
// input, where its output goes, the assignment's preparing deadline, none
// when zero, by which the container must have taken its handoff, and
// what it executes, which labels it.
type Job struct {
	Worker            string
	Run               string
	StateVersion      int64
	Image             string
	Workspace         string
	Mounts            []Mount
	Tools             []Tool
	Volumes           []Volume
	Scoped            []string
	Handoff           []byte
	Output            io.Writer
	PreparingDeadline time.Time
	Execution         Execution
}

// Execution names what a run container executes, as labels whose values
// are bounded: the stack's vendor and the tool its runner calls, each
// Unknown when the specification names another.
type Execution struct {
	Vendor string
	Tool   string
}

// Unknown is an Execution label's value for a name outside its bounds.
const Unknown = "unknown"

// Tool is a verified tool executable at the host path Path, of the
// release Version, which replaces the image's /bin/<Name>; its Mount is
// among the Job's. An executor that cannot mount host paths streams it
// instead.
type Tool struct {
	Name    string
	Version string
	Path    string
}

// Mount is a host path, absolute, that the container sees at Target.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// The kinds of a Volume's object.
const (
	Secret    = "Secret"
	ConfigMap = "ConfigMap"
	PVC       = "PVC"
)

// Volume is an object of the run's cluster namespace that the container
// sees read-only at Target: a Secret's or ConfigMap's key Key as a file,
// or a PersistentVolumeClaim, whose Key is empty, as a directory. An
// executor that runs no Pods starts no job with one.
type Volume struct {
	Kind   string
	Name   string
	Key    string
	Target string
}

// ErrVolume marks a job's mount or volume its executor cannot deliver.
var ErrVolume = errors.New("the executor cannot deliver the mount")

// Exit is how a container ended: its root process's exit code and why it
// ended, as the next poll reports it.
type Exit struct {
	Code   int
	Reason wire.ContainerExitReason
}

// MissingCode is the exit code of a container that is gone, whose real
// code nothing knows.
const MissingCode = -1

// Container is a started run container.
type Container interface {
	// Run is the run the container executes.
	Run() string
	// Wait blocks until the container ends and answers how; a container
	// that is gone ends as wire.Missing.
	Wait(ctx context.Context) (Exit, error)
	// Stop asks the runner to stop with SIGINT, which it passes to the
	// command it runs.
	Stop(ctx context.Context) error
	// Kill ends the container with SIGKILL; its exit is wire.Killed.
	Kill(ctx context.Context) error
	// HandBack opens the workspace of the container, which has ended, to
	// the launcher's user, as its runner does as a run ends, for a
	// container that ended before its runner did, as a killed one.
	HandBack(ctx context.Context) error
	// Remove removes the container, which has ended or is killed first.
	Remove(ctx context.Context) error
}

// Executor starts run containers and finds them again.
//
// ref: decision 0008 § "The launcher and the runner"
type Executor interface {
	// Start starts j's container and hands it j.Handoff.
	Start(ctx context.Context, j Job) (Container, error)
	// Find answers worker's container, running or ended but not removed,
	// copying what it writes from now on to out; false when it has none.
	Find(ctx context.Context, worker string, out io.Writer) (Container, bool, error)
}

// Named is an Executor that names its runtime, such as docker or
// kubernetes, which the launcher logs beside each run it starts.
type Named interface {
	Runtime() string
}

// Command is one initialization command's helper: a fresh container of
// the run's resolved Image, labelled with the worker and run whose
// initialization it serves, its call within that evaluation, the
// private Request frame its helper reads on standard input, and where
// the helper's framed standard output goes. It mounts no workspace,
// tool, credential or other host path but the infraweave executable,
// and receives no environment.
type Command struct {
	Worker       string
	Run          string
	StateVersion int64
	Call         int
	Image        string
	Request      []byte
	Output       io.Writer
}

// ErrImage marks a helper whose image cannot be pulled.
var ErrImage = errors.New("the runner image is unavailable")

// Commander runs initialization command helpers. An executor that is
// one runs them where it runs runs, never on the launcher's host.
//
// ref: decision 0009 § "The worker-side initialization policy"
type Commander interface {
	// Command starts c's helper, writes c.Request to its standard input
	// and closes it, copies its standard output to c.Output until it
	// ends, and removes it, also when ctx ends first, which ends the
	// helper. A write to c.Output that fails ends the helper too.
	Command(ctx context.Context, c Command) error
}
