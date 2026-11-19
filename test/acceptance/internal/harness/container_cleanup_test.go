package harness

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
)

type cleanupDaemon struct {
	calls   []string
	faults  map[string]error
	code    int64
	create  client.ContainerCreateOptions
	filters client.Filters
	blocked bool
	result  *container.WaitExitError
}

func (d *cleanupDaemon) record(op string) error {
	d.calls = append(d.calls, op)
	return d.faults[op]
}

func (d *cleanupDaemon) ContainerList(_ context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
	d.filters = o.Filters
	return client.ContainerListResult{Items: []container.Summary{
		{ID: "run", Labels: map[string]string{docker.LabelWorker: "worker", docker.LabelRun: "run-id"}},
		{ID: "forwarder"},
	}}, d.record("list")
}

func (d *cleanupDaemon) ContainerKill(_ context.Context, id string, o client.ContainerKillOptions) (client.ContainerKillResult, error) {
	if o.Signal != "SIGKILL" {
		return client.ContainerKillResult{}, errors.New("run was not ended before hand-back")
	}
	return client.ContainerKillResult{}, d.record("kill:" + id)
}

func (d *cleanupDaemon) ContainerWait(_ context.Context, id string, _ client.ContainerWaitOptions) client.ContainerWaitResult {
	results, failures := make(chan container.WaitResponse, 1), make(chan error, 1)
	if d.blocked {
		return client.ContainerWaitResult{Result: results, Error: failures}
	}
	if err := d.record("wait:" + id); err != nil {
		failures <- err
	} else {
		code := d.code
		if id == "run" {
			code = 137
		}
		results <- container.WaitResponse{StatusCode: code, Error: d.result}
	}
	return client.ContainerWaitResult{Result: results, Error: failures}
}

func (d *cleanupDaemon) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if slices.Contains(d.calls, "create") && d.faults["inspect-after-create"] != nil {
		return client.ContainerInspectResult{}, d.record("inspect-after-create")
	}
	return client.ContainerInspectResult{
		Container: container.InspectResponse{Image: "sha256:runner", HostConfig: &container.HostConfig{
			Runtime: "runtime", Binds: []string{"/owned/binary:" + docker.BinaryPath + ":ro", "/owned/workspace:" + docker.Workspace},
		}},
	}, d.record(
		"inspect:" + id,
	)
}

func (d *cleanupDaemon) ContainerCreate(_ context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	d.create = o
	op := "create"
	if slices.Contains(d.calls, op) {
		op = "create-retained"
	}
	return client.ContainerCreateResult{ID: "hand-back"}, d.record(op)
}

func (d *cleanupDaemon) ContainerStart(_ context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, d.record("start:" + id)
}

func (d *cleanupDaemon) ContainerRemove(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	return client.ContainerRemoveResult{}, d.record("remove:" + id)
}

func TestUnitScenarioContainersHandBackBeforeRemoval(t *testing.T) {
	d := &cleanupDaemon{}
	if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); err != nil {
		t.Fatal(err)
	}
	want := []string{"list", "kill:run", "wait:run", "inspect:run", "create", "start:hand-back", "wait:hand-back", "remove:hand-back", "remove:run", "remove:forwarder"}
	if !slices.Equal(d.calls, want) || !reflect.DeepEqual(d.filters, client.Filters{}.Add("label", docker.LabelWorker+"=worker")) {
		t.Fatal("cleanup changed scope, raced a live run, skipped hand-back or processed a forwarder as a run", d.calls, d.filters)
	}
	c, h := d.create.Config, d.create.HostConfig
	if c.Image != "sha256:runner" || c.User != docker.User || c.WorkingDir != docker.Workspace || !slices.Equal(c.Entrypoint, []string{docker.BinaryPath}) ||
		!slices.Equal(c.Cmd, []string{"runner", "hand-back"}) || !slices.Equal(h.VolumesFrom, []string{"run"}) || h.NetworkMode != "none" || h.Runtime != "runtime" {
		t.Fatal("hand-back did not retain only the owned run's image, mounts and user", c, h)
	}
}

func TestUnitScenarioContainerCleanupPreservesFailures(t *testing.T) {
	cause := errors.New("native cleanup refused")
	for _, op := range []string{"list", "kill:run", "wait:run", "inspect:run", "create", "start:hand-back", "wait:hand-back", "remove:hand-back", "remove:run"} {
		t.Run(op, func(t *testing.T) {
			d := &cleanupDaemon{faults: map[string]error{op: cause}}
			if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); !errors.Is(err, cause) {
				t.Fatal("cleanup hid a genuine failure", op, err)
			}
			if op != "list" && !slices.Contains(d.calls, "remove:forwarder") {
				t.Fatal("one failed run prevented other scoped cleanup", d.calls)
			}
			if op != "remove:run" && slices.Contains(d.calls, "remove:run") {
				t.Fatal("failed hand-back discarded its original mounts", d.calls)
			}
			if slices.Contains(d.calls, "start:hand-back") && !slices.Contains(d.calls, "remove:hand-back") {
				t.Fatal("failed hand-back leaked its companion", d.calls)
			}
		})
	}
	d := &cleanupDaemon{code: 1}
	if err := removeContainers(
		t.Context(),
		d,
		docker.LabelWorker,
		"worker",
	); err == nil || slices.Contains(d.calls, "remove:run") ||
		!slices.Contains(d.calls, "remove:hand-back") {
		t.Fatal("nonzero hand-back exit was accepted or lost original mounts", err, d.calls)
	}
}

func TestUnitScenarioContainerCleanupAlreadyGone(t *testing.T) {
	for _, op := range []string{"kill:run", "wait:run", "inspect:run", "remove:run", "remove:hand-back"} {
		t.Run(op, func(t *testing.T) {
			d := &cleanupDaemon{faults: map[string]error{op: cerrdefs.ErrNotFound}}
			if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); err != nil || !slices.Contains(d.calls, "remove:forwarder") {
				t.Fatal("an already gone owned container broke cleanup", op, err, d.calls)
			}
		})
	}
	d := &cleanupDaemon{faults: map[string]error{"kill:run": cerrdefs.ErrConflict}}
	if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); err != nil || !slices.Contains(d.calls, "create") {
		t.Fatal("already stopped run skipped hand-back", err, d.calls)
	}
	d = &cleanupDaemon{faults: map[string]error{"create": cerrdefs.ErrNotFound, "inspect-after-create": cerrdefs.ErrNotFound}}
	if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); err != nil ||
		!slices.Contains(d.calls, "create-retained") || !slices.Contains(d.calls, "wait:hand-back") {
		t.Fatal("run removed during creation skipped its retained-bind hand-back", err, d.calls)
	}
	d = &cleanupDaemon{faults: map[string]error{"create": cerrdefs.ErrNotFound}}
	if err := removeContainers(t.Context(), d, docker.LabelWorker, "worker"); !errors.Is(err, cerrdefs.ErrNotFound) || slices.Contains(d.calls, "remove:run") {
		t.Fatal("missing image was confused with an already removed run", err, d.calls)
	}
}

func TestUnitScenarioContainerWaitRefusals(t *testing.T) {
	d := &cleanupDaemon{result: &container.WaitExitError{Message: "daemon wait refused"}}
	if err := waitCleanupContainer(t.Context(), d, "hand-back", true); err == nil || !strings.Contains(err.Error(), "daemon wait refused") {
		t.Fatal("native wait result error was ignored", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d = &cleanupDaemon{blocked: true}
	if err := waitCleanupContainer(ctx, d, "hand-back", true); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled wait did not stop without a container response", err)
	}
}
