package docker_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
)

func command(out *bytes.Buffer) executor.Command {
	c := executor.Command{
		Worker: "w", Run: "r", StateVersion: 2, Call: 1, Image: "img", Request: []byte("private-argv"),
	}
	if out != nil {
		c.Output = out
	}
	return c
}

// TestUnitDockerCommandRunsAnIsolatedHelper checks an initialization
// command's helper on a fake daemon: a container of the run's image
// whose root process is the mounted binary as runner command, as UID
// 1983, with no capability, environment or mount but the binary, whose
// standard input alone carries the request and whose standard output is
// copied, removed once it ends.
func TestUnitDockerCommandRunsAnIsolatedHelper(t *testing.T) {
	d := &daemon{output: []byte("frames")}
	e := docker.NewWith(d, "/bin/infraweave", docker.Config{Network: "net", Runtime: "runsc"})
	var out bytes.Buffer
	if err := e.Command(t.Context(), command(&out)); err != nil {
		t.Fatal(err)
	}
	if out.String() != "frames" || d.got.String() != "private-argv" || d.removed != 1 {
		t.Fatalf("output %q, stdin %q, removed %d", out.String(), d.got.String(), d.removed)
	}
	cfg, host := d.created, d.host
	if cfg.Image != "img" || cfg.User != docker.User || !slices.Equal(cfg.Entrypoint, []string{docker.BinaryPath}) ||
		!slices.Equal(cfg.Cmd, []string{"runner", "command"}) || len(cfg.Env) != 0 || !cfg.OpenStdin || !cfg.StdinOnce ||
		cfg.Labels[docker.LabelCommandWorker] != "w" || cfg.Labels[docker.LabelCommandRun] != "r" ||
		cfg.Labels[docker.LabelWorker] != "" || cfg.Labels[docker.LabelRun] != "" {
		t.Errorf("config %+v", cfg)
	}
	if !slices.Equal(host.Binds, []string{"/bin/infraweave:" + docker.BinaryPath + ":ro"}) || !slices.Equal(host.CapDrop, []string{"ALL"}) ||
		!slices.Equal(host.SecurityOpt, []string{"no-new-privileges"}) || host.Memory <= 0 || host.PidsLimit == nil ||
		host.NetworkMode != "net" || host.Runtime != "runsc" || host.Privileged {
		t.Errorf("host config %+v", host)
	}
	if strings.Contains(strings.Join(append(cfg.Cmd, cfg.Entrypoint...), " "), "private-argv") {
		t.Error("the request reached the container's configuration")
	}
	if err := docker.NewWith(&daemon{output: []byte{}}, "bin", docker.Config{}).Command(t.Context(), command(nil)); err != nil {
		t.Errorf("a helper without an output writer: %v", err)
	}
}

func TestUnitDockerCommandRemovesItsHelperHoweverItEnds(t *testing.T) {
	for name, c := range map[string]struct {
		d       *daemon
		want    string
		removed int
	}{
		"pull":   {&daemon{missing: true, errs: map[string]error{"pull": errDaemon}}, "pull image img", 0},
		"create": {&daemon{errs: map[string]error{"create": errDaemon}}, "create run r's initialization command", 0},
		"attach": {&daemon{errs: map[string]error{"attach": errDaemon}}, "attach to run r's", 1},
		"start":  {&daemon{errs: map[string]error{"start": errDaemon}}, "start run r's", 1},
		"write":  {&daemon{errs: map[string]error{"write": errDaemon}}, "send run r's", 1},
		"remove": {&daemon{output: []byte("x"), errs: map[string]error{"remove": errDaemon}}, "remove run r's", 1},
	} {
		err := docker.NewWith(c.d, "bin", docker.Config{}).Command(t.Context(), command(nil))
		if err == nil || !strings.Contains(err.Error(), c.want) || c.d.removed != c.removed {
			t.Errorf("%s: %v, removed %d", name, err, c.d.removed)
		}
	}
	missing := &daemon{missing: true, errs: map[string]error{"pull-wait": errDaemon}}
	if err := docker.NewWith(missing, "bin", docker.Config{}).Command(t.Context(), command(nil)); !errors.Is(err, executor.ErrImage) {
		t.Errorf("an unpullable image = %v", err)
	}
	gone := &daemon{output: []byte("x"), errs: map[string]error{"remove": cerrdefs.ErrNotFound}}
	if err := docker.NewWith(gone, "bin", docker.Config{}).Command(t.Context(), command(nil)); err != nil {
		t.Errorf("a helper already gone = %v", err)
	}

	requested := make(chan struct{})
	held := &daemon{hold: true, requested: requested}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-requested
		cancel()
	}()
	if err := docker.NewWith(held, "bin", docker.Config{}).Command(ctx, command(nil)); !errors.Is(err, context.Canceled) || held.removed != 1 {
		t.Errorf("a cancelled helper = %v, removed %d", err, held.removed)
	}

	refusing := &daemon{output: []byte("frames")}
	if err := docker.NewWith(refusing, "bin", docker.Config{}).Command(t.Context(), executor.Command{Run: "r", Output: failingWriter{}}); err == nil || refusing.removed != 1 {
		t.Errorf("an output the reader refused = %v, removed %d", err, refusing.removed)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errDaemon }

// TestUnitDockerFindRemovesLeftHelpers checks that a restarted launcher's
// Find removes its worker's helpers and never answers one as its run.
func TestUnitDockerFindRemovesLeftHelpers(t *testing.T) {
	items := []container.Summary{
		{ID: "helper", Created: 3, Labels: map[string]string{docker.LabelCommandWorker: "w", docker.LabelCommandRun: "r"}},
		{ID: "other", Created: 4, Labels: map[string]string{docker.LabelCommandWorker: "x"}},
	}
	d := &daemon{items: items}
	if _, ok, err := docker.NewWith(d, "bin", docker.Config{}).Find(t.Context(), "w", nil); ok || err != nil || d.removed != 1 {
		t.Fatalf("Find = %v, %v, removed %d", ok, err, d.removed)
	}
	for call, want := range map[string]string{"list": "list worker w's initialization", "remove": "remove worker w's initialization"} {
		d := &daemon{items: items, errs: map[string]error{call: errDaemon}}
		if _, _, err := docker.NewWith(d, "bin", docker.Config{}).Find(t.Context(), "w", nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", call, err)
		}
	}
	gone := &daemon{items: items, errs: map[string]error{"remove": cerrdefs.ErrNotFound}}
	if _, _, err := docker.NewWith(gone, "bin", docker.Config{}).Find(t.Context(), "w", nil); err != nil {
		t.Errorf("a helper already gone: %v", err)
	}
}
