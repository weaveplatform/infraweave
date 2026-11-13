package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// The run container's contract with its image: the launcher's binary at
// BinaryPath, run with the argument runner as the root process, as User,
// in Workspace. An image that has a user with UID 1983 and does not rely
// on ENTRYPOINT or CMD runs unchanged, as Spacelift's images do.
//
// ref: decision 0008 § "The launcher and the runner"
const (
	BinaryPath = "/opt/infraweave/bin/infraweave"
	User       = "1983:1983"
	Workspace  = "/mnt/workspace"
)

// The labels that find a worker's container again.
const (
	LabelWorker = "io.infraweave.worker"
	LabelRun    = "io.infraweave.run"
)

// Config is how the executor starts containers.
type Config struct {
	// Binary is the executable mounted at BinaryPath: the launcher's own
	// when empty, which is statically linked so it runs on any image's
	// libc.
	Binary string
	// Network and Runtime are each container's network and OCI runtime;
	// Docker's defaults when empty.
	Network string
	Runtime string
}

// api is the part of the moby client the executor calls; *client.Client
// is one.
type api interface {
	Ping(ctx context.Context, o client.PingOptions) (client.PingResult, error)
	ImageInspect(
		ctx context.Context, ref string, o ...client.ImageInspectOption,
	) (client.ImageInspectResult, error)
	ImagePull(
		ctx context.Context,
		ref string,
		o client.ImagePullOptions,
	) (client.ImagePullResponse, error)
	ContainerCreate(
		ctx context.Context,
		o client.ContainerCreateOptions,
	) (client.ContainerCreateResult, error)
	ContainerAttach(
		ctx context.Context, id string, o client.ContainerAttachOptions,
	) (client.ContainerAttachResult, error)
	ContainerStart(
		ctx context.Context, id string, o client.ContainerStartOptions,
	) (client.ContainerStartResult, error)
	ContainerList(
		ctx context.Context,
		o client.ContainerListOptions,
	) (client.ContainerListResult, error)
	ContainerLogs(
		ctx context.Context,
		id string,
		o client.ContainerLogsOptions,
	) (client.ContainerLogsResult, error)
	ContainerWait(
		ctx context.Context,
		id string,
		o client.ContainerWaitOptions,
	) client.ContainerWaitResult
	ContainerInspect(
		ctx context.Context, id string, o client.ContainerInspectOptions,
	) (client.ContainerInspectResult, error)
	ContainerKill(
		ctx context.Context,
		id string,
		o client.ContainerKillOptions,
	) (client.ContainerKillResult, error)
	ContainerRemove(
		ctx context.Context, id string, o client.ContainerRemoveOptions,
	) (client.ContainerRemoveResult, error)
	Close() error
}

// Executor is the Docker executor.Executor.
type Executor struct {
	cli    api
	binary string
	c      Config
}

var _ executor.Executor = (*Executor)(nil)

// errWait marks a container whose end the daemon could not report.
var errWait = errors.New("docker: wait for the container")

// New is an executor on the Docker daemon the DOCKER_* environment
// names, or the local socket.
func New(c Config) (*Executor, error) {
	bin := c.Binary
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("docker executor: the launcher's own binary: %w", err)
		}
		bin = self
	}
	bin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, fmt.Errorf("docker executor: the runner binary: %w", err)
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker executor: %w", err)
	}
	return newExecutor(cli, bin, c), nil
}

func newExecutor(cli api, binary string, c Config) *Executor {
	return &Executor{cli: cli, binary: binary, c: c}
}

// Close closes the connection to the daemon.
func (e *Executor) Close() error { return e.cli.Close() } //nolint:wrapcheck // closing

// Ping checks that the daemon answers, for the launcher's readiness.
func (e *Executor) Ping(ctx context.Context) error {
	if _, err := e.cli.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	return nil
}

// Start pulls j's image when the host lacks it, creates its container
// with the launcher's binary mounted read-only as its root process, j's
// workspace at Workspace and j's mounts, starts it, writes j.Handoff to its standard input and
// closes it, and copies its output to j.Output until it ends.
func (e *Executor) Start(ctx context.Context, j executor.Job) (executor.Container, error) {
	if err := e.image(ctx, j.Image); err != nil {
		return nil, err
	}
	binds := make([]string, 0, 2+len(j.Mounts))
	binds = append(binds, e.binary+":"+BinaryPath+":ro")
	if j.Workspace != "" {
		binds = append(binds, j.Workspace+":"+Workspace)
	}
	for _, m := range j.Mounts {
		bind := m.Source + ":" + m.Target
		if m.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}
	created, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        j.Image,
			User:         User,
			Entrypoint:   []string{BinaryPath},
			Cmd:          []string{"runner"},
			WorkingDir:   Workspace,
			Labels:       map[string]string{LabelWorker: j.Worker, LabelRun: j.Run},
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			OpenStdin:    true,
			StdinOnce:    true,
		},
		HostConfig: &container.HostConfig{
			Binds:       binds,
			NetworkMode: container.NetworkMode(e.c.Network),
			Runtime:     e.c.Runtime,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("docker: create run %s's container: %w", j.Run, err)
	}
	c := &run{e: e, id: created.ID, run: j.Run}
	// The stream outlives this call, so it is not bound to ctx.
	att, err := e.cli.ContainerAttach(
		context.WithoutCancel(ctx),
		c.id,
		client.ContainerAttachOptions{
			Stream: true, Stdin: true, Stdout: true, Stderr: true,
		},
	)
	if err != nil {
		return nil, e.abandon(
			ctx,
			c,
			fmt.Errorf("docker: attach to run %s's container: %w", j.Run, err),
		)
	}
	c.copied = copyOutput(j.Output, att.Reader, att.Close)
	if _, err := e.cli.ContainerStart(ctx, c.id, client.ContainerStartOptions{}); err != nil {
		att.Close()
		return nil, e.abandon(
			ctx,
			c,
			fmt.Errorf("docker: start run %s's container: %w", j.Run, err),
		)
	}
	if _, err := att.Conn.Write(j.Handoff); err != nil {
		return nil, e.abandon(ctx, c, fmt.Errorf("docker: hand run %s over: %w", j.Run, err))
	}
	if err := att.CloseWrite(); err != nil {
		return nil, e.abandon(ctx, c, fmt.Errorf("docker: hand run %s over: %w", j.Run, err))
	}
	return c, nil
}

// abandon removes c, which failed to start, and answers err.
func (e *Executor) abandon(ctx context.Context, c *run, err error) error {
	return errors.Join(err, c.Remove(context.WithoutCancel(ctx)))
}

// image pulls ref unless the host has it.
func (e *Executor) image(ctx context.Context, ref string) error {
	_, err := e.cli.ImageInspect(ctx, ref)
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: inspect image %s: %w", ref, err)
	}
	pull, err := e.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("docker: pull image %s: %w", ref, err)
	}
	if err := pull.Wait(ctx); err != nil {
		return fmt.Errorf("docker: pull image %s: %w", ref, err)
	}
	return nil
}

// Find answers worker's newest container, running or ended, copying what
// it writes from now on to out.
func (e *Executor) Find(
	ctx context.Context,
	worker string,
	out io.Writer,
) (executor.Container, bool, error) {
	list, err := e.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", LabelWorker+"="+worker),
	})
	if err != nil {
		return nil, false, fmt.Errorf("docker: list worker %s's containers: %w", worker, err)
	}
	if len(list.Items) == 0 {
		return nil, false, nil
	}
	newest := slices.MaxFunc(list.Items, func(a, b container.Summary) int {
		return cmp.Compare(a.Created, b.Created)
	})
	c := &run{e: e, id: newest.ID, run: newest.Labels[LabelRun]}
	logs, err := e.cli.ContainerLogs(context.WithoutCancel(ctx), c.id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true,
		Since: strconv.FormatInt(time.Now().Unix(), 10),
	})
	if err != nil {
		return nil, false, fmt.Errorf("docker: follow run %s's output: %w", c.run, err)
	}
	c.copied = copyOutput(out, logs, func() { _ = logs.Close() })
	return c, true, nil
}

// copyOutput copies the multiplexed stream src to out until it ends,
// then calls done, and closes the channel it answers.
func copyOutput(out io.Writer, src io.Reader, done func()) <-chan struct{} {
	if out == nil {
		out = io.Discard
	}
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		defer done()
		// The stream ends with the container; an error only cuts the
		// copy of its output short.
		_, _ = stdcopy.StdCopy(out, out, src)
	}()
	return copied
}

// run is a run's container.
type run struct {
	e      *Executor
	id     string
	run    string
	killed atomic.Bool
	copied <-chan struct{}
}

func (c *run) Run() string { return c.run }

// Wait waits for the container to stop, then for its output to be
// copied, and answers its exit: killed when Kill ended it, out of memory
// when the kernel did, missing when it is gone.
func (c *run) Wait(ctx context.Context) (executor.Exit, error) {
	w := c.e.cli.ContainerWait(ctx, c.id, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	var code int
	select {
	case res := <-w.Result:
		if res.Error != nil {
			return executor.Exit{}, fmt.Errorf("%w: run %s: %s", errWait, c.run, res.Error.Message)
		}
		code = int(res.StatusCode)
	case err := <-w.Error:
		if cerrdefs.IsNotFound(err) {
			return executor.Exit{Code: executor.MissingCode, Reason: wire.Missing}, nil
		}
		return executor.Exit{}, fmt.Errorf("docker: wait for run %s: %w", c.run, err)
	}
	select {
	case <-c.copied:
	case <-ctx.Done():
		return executor.Exit{}, fmt.Errorf("docker: copy run %s's output: %w", c.run, ctx.Err())
	}
	if c.killed.Load() {
		return executor.Exit{Code: code, Reason: wire.Killed}, nil
	}
	in, err := c.e.cli.ContainerInspect(ctx, c.id, client.ContainerInspectOptions{})
	switch {
	case cerrdefs.IsNotFound(err):
		return executor.Exit{Code: code, Reason: wire.Missing}, nil
	case err != nil:
		return executor.Exit{}, fmt.Errorf("docker: inspect run %s: %w", c.run, err)
	case in.Container.State != nil && in.Container.State.OOMKilled:
		return executor.Exit{Code: code, Reason: wire.OutOfMemory}, nil
	}
	return executor.Exit{Code: code, Reason: wire.Exited}, nil
}

// Stop sends the runner SIGINT. A container that has stopped already is
// not an error.
func (c *run) Stop(ctx context.Context) error { return c.signal(ctx, "SIGINT") }

// Kill sends the container SIGKILL, so its exit is killed.
func (c *run) Kill(ctx context.Context) error {
	c.killed.Store(true)
	return c.signal(ctx, "SIGKILL")
}

func (c *run) signal(ctx context.Context, sig string) error {
	_, err := c.e.cli.ContainerKill(ctx, c.id, client.ContainerKillOptions{Signal: sig})
	if err != nil && !cerrdefs.IsConflict(err) && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: send run %s %s: %w", c.run, sig, err)
	}
	return nil
}

// Remove removes the container, killing it first when it runs. A
// container that is gone is not an error.
func (c *run) Remove(ctx context.Context) error {
	_, err := c.e.cli.ContainerRemove(ctx, c.id, client.ContainerRemoveOptions{Force: true})
	if err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("docker: remove run %s's container: %w", c.run, err)
	}
	return nil
}
