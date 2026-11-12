package agenttest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Executor is a fake executor.Executor whose containers run until the
// test ends them. It keeps every container it started, by worker, until
// it is removed, so a launcher restarted on the same Executor finds its
// worker's container again.
type Executor struct {
	mu         sync.Mutex
	jobs       []executor.Job
	containers map[string]*Container
	startErr   error
	findErr    error
}

// NewExecutor is an Executor with no containers.
func NewExecutor() *Executor { return &Executor{containers: map[string]*Container{}} }

// FailStart makes every later Start fail with err; nil starts again.
func (e *Executor) FailStart(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.startErr = err
}

// FailFind makes every later Find fail with err; nil finds again.
func (e *Executor) FailFind(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.findErr = err
}

// Jobs are the jobs Start was given, in order.
func (e *Executor) Jobs() []executor.Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executor.Job(nil), e.jobs...)
}

// Container is worker's container, nil when it has none.
func (e *Executor) Container(worker string) *Container {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.containers[worker]
}

// Start records j, with a copy of its handoff, which the launcher clears
// once Start returns, and answers its container, running.
func (e *Executor) Start(_ context.Context, j executor.Job) (executor.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.startErr != nil {
		return nil, e.startErr
	}
	j.Handoff = bytes.Clone(j.Handoff)
	e.jobs = append(e.jobs, j)
	c := &Container{e: e, worker: j.Worker, run: j.Run, ended: make(chan executor.Exit, 1)}
	e.containers[j.Worker] = c
	return c, nil
}

// Find answers worker's container.
func (e *Executor) Find(
	_ context.Context,
	worker string,
	_ io.Writer,
) (executor.Container, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.findErr != nil {
		return nil, false, e.findErr
	}
	c, ok := e.containers[worker]
	return c, ok, nil
}

// Container is a fake run container.
type Container struct {
	e      *Executor
	worker string
	run    string
	ended  chan executor.Exit

	mu      sync.Mutex
	stopped int
	killed  bool
	removed bool
	exit    *executor.Exit
	err     error
}

// End ends the container with exit; a container ends once.
func (c *Container) End(exit executor.Exit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exit == nil {
		c.exit = &exit
		c.ended <- exit
	}
}

// Fail makes Wait answer err, as when the executor cannot read the
// container's end.
func (c *Container) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exit == nil {
		c.exit, c.err = &executor.Exit{}, err
		c.ended <- executor.Exit{}
	}
}

// Stopped, Killed and Removed report what the launcher did to the
// container.
func (c *Container) Stopped() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

func (c *Container) Killed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.killed
}

func (c *Container) Removed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removed
}

func (c *Container) Run() string { return c.run }

// Wait answers the container's exit once it ends; an ended container
// answers it again.
func (c *Container) Wait(ctx context.Context) (executor.Exit, error) {
	c.mu.Lock()
	if c.exit != nil {
		defer c.mu.Unlock()
		return *c.exit, c.err
	}
	c.mu.Unlock()
	select {
	case <-c.ended:
		c.mu.Lock()
		defer c.mu.Unlock()
		return *c.exit, c.err
	case <-ctx.Done():
		return executor.Exit{}, fmt.Errorf("agenttest: wait: %w", ctx.Err())
	}
}

// Stop counts a SIGINT; the test decides when the container ends.
func (c *Container) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped++
	return nil
}

// Kill ends the container as killed.
func (c *Container) Kill(context.Context) error {
	c.mu.Lock()
	c.killed = true
	c.mu.Unlock()
	c.End(executor.Exit{Code: 137, Reason: wire.Killed})
	return nil
}

// Remove removes the container, ending it as killed when it runs.
func (c *Container) Remove(context.Context) error {
	c.End(executor.Exit{Code: 137, Reason: wire.Killed})
	c.mu.Lock()
	c.removed = true
	c.mu.Unlock()
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	if c.e.containers[c.worker] == c {
		delete(c.e.containers, c.worker)
	}
	return nil
}
