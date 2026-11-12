package docker_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"net"
	"strings"
	"sync"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// daemon is a fake Docker daemon: errs fails the call it names, missing
// makes the image absent, and the rest shape what a container's wait and
// inspect answer.
type daemon struct {
	mu      sync.Mutex
	errs    map[string]error
	missing bool
	oom     bool
	wait    container.WaitResponse
	waitErr error
	logs    io.ReadCloser
	items   []container.Summary
	created container.Config
	host    container.HostConfig
	removed int
	stdin   net.Conn
}

func (d *daemon) err(call string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.errs[call]
}

func (d *daemon) Ping(context.Context, client.PingOptions) (client.PingResult, error) {
	return client.PingResult{}, d.err("ping")
}

func (d *daemon) ImageInspect(
	context.Context, string, ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	if d.missing {
		return client.ImageInspectResult{}, cerrdefs.ErrNotFound
	}
	return client.ImageInspectResult{}, d.err("inspect-image")
}

type pulled struct {
	io.ReadCloser
	err error
}

func (p pulled) JSONMessages(context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(func(jsonstream.Message, error) bool) {}
}

func (p pulled) Wait(context.Context) error { return p.err }

func (d *daemon) ImagePull(
	context.Context,
	string,
	client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	if err := d.err("pull"); err != nil {
		return nil, err
	}
	return pulled{io.NopCloser(strings.NewReader("")), d.err("pull-wait")}, nil
}

func (d *daemon) ContainerCreate(
	_ context.Context, o client.ContainerCreateOptions,
) (client.ContainerCreateResult, error) {
	d.created, d.host = *o.Config, *o.HostConfig
	return client.ContainerCreateResult{ID: "c1"}, d.err("create")
}

func (d *daemon) ContainerAttach(
	context.Context, string, client.ContainerAttachOptions,
) (client.ContainerAttachResult, error) {
	if err := d.err("attach"); err != nil {
		return client.ContainerAttachResult{}, err
	}
	ours, theirs := net.Pipe()
	d.stdin = theirs
	if d.err("write") != nil {
		_ = theirs.Close()
	} else {
		go func() { _, _ = io.Copy(io.Discard, theirs) }()
	}
	return client.ContainerAttachResult{HijackedResponse: client.NewHijackedResponse(ours, "")}, nil
}

func (d *daemon) ContainerStart(
	context.Context, string, client.ContainerStartOptions,
) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, d.err("start")
}

func (d *daemon) ContainerList(
	context.Context,
	client.ContainerListOptions,
) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: d.items}, d.err("list")
}

func (d *daemon) ContainerLogs(
	context.Context, string, client.ContainerLogsOptions,
) (client.ContainerLogsResult, error) {
	if err := d.err("logs"); err != nil {
		return nil, err
	}
	if d.logs != nil {
		return d.logs, nil
	}
	return io.NopCloser(strings.NewReader("")), nil
}

// ContainerWait ends the container's output stream, then answers.
func (d *daemon) ContainerWait(
	context.Context,
	string,
	client.ContainerWaitOptions,
) client.ContainerWaitResult {
	if d.stdin != nil {
		_ = d.stdin.Close()
	}
	res, errs := make(chan container.WaitResponse, 1), make(chan error, 1)
	if d.waitErr != nil {
		errs <- d.waitErr
	} else {
		res <- d.wait
	}
	return client.ContainerWaitResult{Result: res, Error: errs}
}

func (d *daemon) ContainerInspect(
	context.Context, string, client.ContainerInspectOptions,
) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{
		Container: container.InspectResponse{State: &container.State{OOMKilled: d.oom}},
	}, d.err("inspect")
}

func (d *daemon) ContainerKill(
	context.Context,
	string,
	client.ContainerKillOptions,
) (client.ContainerKillResult, error) {
	return client.ContainerKillResult{}, d.err("kill")
}

func (d *daemon) ContainerRemove(
	context.Context, string, client.ContainerRemoveOptions,
) (client.ContainerRemoveResult, error) {
	d.mu.Lock()
	d.removed++
	d.mu.Unlock()
	return client.ContainerRemoveResult{}, d.err("remove")
}

func (d *daemon) Close() error { return nil }

var errDaemon = errors.New("the daemon refused")

// TestUnitDockerExecutorFailures checks the executor's answers to a
// daemon that fails, on a fake daemon: each step of a start that fails
// names it, and a container created before the failure is removed; the
// container is created with the launcher's binary as its root process; a
// wait answers the daemon's errors, a gone container as missing and the
// kernel's kill as out-of-memory; a signal or a remove the daemon refuses
// fails, and one for a container that has ended or gone does not.
func TestUnitDockerExecutorFailures(t *testing.T) {
	ctx := t.Context()
	job := executor.Job{Worker: "w", Run: "r", Image: "img", Handoff: []byte("{}")}

	d := &daemon{missing: true}
	e := docker.NewWith(
		d,
		"/usr/local/bin/infraweave",
		docker.Config{Network: "runs", Runtime: "runsc"},
	)
	c, err := e.Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(append(d.created.Entrypoint, d.created.Cmd...), " "); got !=
		docker.BinaryPath+" runner" || d.created.User != docker.User ||
		d.created.WorkingDir != docker.Workspace || d.created.Labels[docker.LabelRun] != "r" ||
		d.host.Binds[0] != "/usr/local/bin/infraweave:"+docker.BinaryPath+":ro" ||
		d.host.NetworkMode != "runs" || d.host.Runtime != "runsc" {
		t.Errorf("created %+v with %+v", d.created, d.host)
	}
	if c.Run() != "r" {
		t.Errorf("Run = %q", c.Run())
	}
	if exit, err := c.Wait(ctx); err != nil || exit != (executor.Exit{Reason: wire.Exited}) {
		t.Errorf("Wait = %+v, %v", exit, err)
	}

	for _, tc := range []struct {
		call, want string
		missing    bool
		removed    int
	}{
		{call: "inspect-image", want: "inspect image img"},
		{call: "pull", want: "pull image img", missing: true},
		{call: "pull-wait", want: "pull image img", missing: true},
		{call: "create", want: "create run r's container"},
		{call: "attach", want: "attach to run r's container", removed: 1},
		{call: "start", want: "start run r's container", removed: 1},
		{call: "write", want: "hand run r over", removed: 1},
	} {
		d := &daemon{errs: map[string]error{tc.call: errDaemon}, missing: tc.missing}
		_, err := docker.NewWith(d, "bin", docker.Config{}).Start(ctx, job)
		if err == nil || !strings.Contains(err.Error(), tc.want) || d.removed != tc.removed {
			t.Errorf("%s: Start = %v, %d removed", tc.call, err, d.removed)
		}
	}

	for _, tc := range []struct {
		name string
		d    *daemon
		want executor.Exit
		err  string
	}{
		{"wait error", &daemon{wait: container.WaitResponse{Error: &container.WaitExitError{
			Message: "no exit",
		}}}, executor.Exit{}, "no exit"},
		{
			"gone", &daemon{waitErr: cerrdefs.ErrNotFound},
			executor.Exit{Code: executor.MissingCode, Reason: wire.Missing},
			"",
		},
		{"wait refused", &daemon{waitErr: errDaemon}, executor.Exit{}, "wait for run r"},
		{
			"gone after", &daemon{
				wait: container.WaitResponse{StatusCode: 2},
				errs: map[string]error{"inspect": cerrdefs.ErrNotFound},
			},
			executor.Exit{Code: 2, Reason: wire.Missing},
			"",
		},
		{
			"inspect refused", &daemon{errs: map[string]error{"inspect": errDaemon}},
			executor.Exit{},
			"inspect run r",
		},
		{
			"out of memory", &daemon{oom: true, wait: container.WaitResponse{StatusCode: 137}},
			executor.Exit{Code: 137, Reason: wire.OutOfMemory},
			"",
		},
	} {
		c, err := docker.NewWith(tc.d, "bin", docker.Config{}).Start(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		exit, err := c.Wait(ctx)
		if exit != tc.want || (tc.err == "") != (err == nil) ||
			(err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: Wait = %+v, %v", tc.name, exit, err)
		}
	}

	refusing := &daemon{errs: map[string]error{"kill": errDaemon, "remove": errDaemon}}
	c, err = docker.NewWith(refusing, "bin", docker.Config{}).Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stop(ctx) == nil || c.Kill(ctx) == nil || c.Remove(ctx) == nil {
		t.Error("a refused signal or remove did not fail")
	}
	ended := &daemon{
		errs: map[string]error{"kill": cerrdefs.ErrConflict, "remove": cerrdefs.ErrNotFound},
	}
	c, err = docker.NewWith(ended, "bin", docker.Config{}).Start(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(c.Stop(ctx), c.Kill(ctx), c.Remove(ctx)); err != nil {
		t.Errorf("a signal or remove of an ended container = %v", err)
	}
	if exit, _ := c.Wait(ctx); exit.Reason != wire.Killed {
		t.Errorf("a killed container's exit = %+v", exit)
	}
}

// TestUnitDockerExecutorFind checks Find and Ping on a fake daemon: the
// newest of a worker's containers is found with its run, a refused list
// or log follow fails, a wait whose output is still being copied ends with
// its context, and a daemon that does not answer fails the ping.
func TestUnitDockerExecutorFind(t *testing.T) {
	ctx := t.Context()
	items := []container.Summary{
		{ID: "old", Created: 1, Labels: map[string]string{docker.LabelRun: "r1"}},
		{ID: "new", Created: 2, Labels: map[string]string{docker.LabelRun: "r2"}},
	}
	follow, _ := io.Pipe()
	d := &daemon{items: items, logs: follow}
	e := docker.NewWith(d, "bin", docker.Config{})
	c, ok, err := e.Find(ctx, "w", nil)
	if err != nil || !ok || c.Run() != "r2" {
		t.Fatalf("Find = %v, %v, %v", c, ok, err)
	}
	cut, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Wait(cut); !errors.Is(err, context.Canceled) {
		t.Errorf("Wait with output still copying = %v", err)
	}
	for call, want := range map[string]string{"list": "list worker w's", "logs": "follow run r2's"} {
		d := &daemon{items: items, errs: map[string]error{call: errDaemon}}
		if _, _, err := docker.NewWith(d, "bin", docker.Config{}).Find(ctx, "w", nil); err == nil ||
			!strings.Contains(err.Error(), want) {
			t.Errorf("%s: Find = %v", call, err)
		}
	}
	if err := docker.NewWith(&daemon{errs: map[string]error{"ping": errDaemon}}, "bin",
		docker.Config{}).Ping(ctx); !errors.Is(err, errDaemon) {
		t.Errorf("Ping = %v", err)
	}
	if err := e.Close(); err != nil {
		t.Error(err)
	}
	if _, err := docker.New(docker.Config{Binary: "/no/such/infraweave"}); err == nil {
		t.Error("New with a binary that does not exist")
	}
}
