package executor

import (
	"context"
	"io"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Job is one run's container: the worker and the run it belongs to, which
// label it so a restarted launcher finds it, the image it starts from, the
// host directory that is its workspace, the image's own when empty, the
// other host paths it mounts, the handoff its runner reads on standard
// input, and where its output goes.
type Job struct {
	Worker    string
	Run       string
	Image     string
	Workspace string
	Mounts    []Mount
	Handoff   []byte
	Output    io.Writer
}

// Mount is a host path, absolute, that the container sees at Target.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

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
