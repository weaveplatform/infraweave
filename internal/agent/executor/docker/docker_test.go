package docker_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// output is a writer the copy goroutine and the test share.
type output struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p) //nolint:wrapcheck // a buffer
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

// TestAccDockerExecutor checks the run container on a real Docker daemon,
// with a probe in place of the launcher's binary and an image holding
// nothing but the workspace directory, so the test pulls nothing. The
// container's root process is the binary at /opt/infraweave/bin/infraweave
// with the argument runner, as UID and GID 1983, in /mnt/workspace, and
// reads the handoff on standard input; its exit code is answered. A
// restarted launcher finds its worker's running container and follows its
// output; Stop interrupts it, Kill ends it as killed, a container removed
// behind the executor's back is missing, and an image the host lacks and
// cannot pull fails the start.
func TestAccDockerExecutor(t *testing.T) {
	ctx := t.Context()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	image := emptyImage(t, cli)
	e, err := docker.New(docker.Config{Binary: probe(t, cli)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if err := e.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	worker := kernel.NewULID().String()
	start := func(t *testing.T, handoff string, out *output) executor.Container {
		t.Helper()
		var w io.Writer
		if out != nil {
			w = out
		}
		c, err := e.Start(ctx, executor.Job{
			Worker: worker, Run: kernel.NewULID().String(), Image: image,
			Handoff: []byte(handoff), Output: w,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Remove(context.WithoutCancel(ctx)) })
		return c
	}

	t.Run("the runner's process", func(t *testing.T) {
		var out output
		c := start(t, "3", &out)
		exit, err := c.Wait(ctx)
		if err != nil || exit != (executor.Exit{Code: 3, Reason: wire.Exited}) {
			t.Fatalf("Wait = %+v, %v", exit, err)
		}
		var got struct {
			UID   int      `json:"uid"`
			GID   int      `json:"gid"`
			PID   int      `json:"pid"`
			Args  []string `json:"args"`
			WD    string   `json:"wd"`
			Stdin string   `json:"stdin"`
		}
		if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
			t.Fatalf("output %q: %v", out.String(), err)
		}
		if got.UID != 1983 || got.GID != 1983 || got.PID != 1 || got.WD != docker.Workspace ||
			got.Stdin != "3" ||
			strings.Join(got.Args, " ") != docker.BinaryPath+" runner" {
			t.Errorf("the runner ran as %+v", got)
		}
		if err := c.Remove(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("found again, stopped, killed and missing", func(t *testing.T) {
		start(t, "wait", nil)
		var out output
		found, ok, err := e.Find(ctx, worker, &out)
		if err != nil || !ok {
			t.Fatalf("Find = %v, %v", ok, err)
		}
		if err := found.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		exit, err := found.Wait(ctx)
		if err != nil || exit != (executor.Exit{Code: 130, Reason: wire.Exited}) ||
			!strings.Contains(out.String(), "interrupted") {
			t.Fatalf("a stopped runner: %+v, %v, output %q", exit, err, out.String())
		}
		if err := found.Stop(ctx); err != nil {
			t.Errorf("Stop of an ended container = %v", err)
		}
		if err := found.Remove(ctx); err != nil {
			t.Fatal(err)
		}

		killed := start(t, "wait", nil)
		if err := killed.Kill(ctx); err != nil {
			t.Fatal(err)
		}
		if exit, err := killed.Wait(ctx); err != nil || exit.Reason != wire.Killed {
			t.Errorf("a killed runner: %+v, %v", exit, err)
		}

		gone := start(t, "wait", nil)
		if err := gone.Remove(ctx); err != nil {
			t.Fatal(err)
		}
		if exit, err := gone.Wait(ctx); err != nil ||
			exit != (executor.Exit{Code: executor.MissingCode, Reason: wire.Missing}) {
			t.Errorf("a removed container: %+v, %v", exit, err)
		}
		if err := gone.Remove(ctx); err != nil {
			t.Errorf("Remove of a gone container = %v", err)
		}
		if _, ok, err := e.Find(ctx, kernel.NewULID().String(), nil); ok || err != nil {
			t.Errorf("Find of another worker = %v, %v", ok, err)
		}
	})

	t.Run("an image the host cannot pull", func(t *testing.T) {
		// The daemon retries an unreachable registry for longer than the
		// test needs to see the pull fail.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, err := e.Start(ctx, executor.Job{
			Worker: worker, Run: kernel.NewULID().String(), Image: "localhost:1/infraweave/none",
		}); err == nil || !strings.Contains(err.Error(), "pull image") {
			t.Errorf("Start = %v", err)
		}
	})
}

// emptyImage imports an image whose filesystem holds the workspace
// directory alone, and removes it when the test ends.
func emptyImage(t *testing.T, cli *client.Client) string {
	t.Helper()
	var root bytes.Buffer
	tw := tar.NewWriter(&root)
	for _, dir := range []string{"mnt/", "mnt/workspace/"} {
		if err := tw.WriteHeader(
			&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o777},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	ref := "infraweave-executor-test:" + strings.ToLower(kernel.NewULID().String())
	res, err := cli.ImageImport(
		t.Context(),
		client.ImageImportSource{Source: &root, SourceName: "-"},
		ref,
		client.ImageImportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = res.Read(make([]byte, 4096))
	_ = res.Close()
	t.Cleanup(func() {
		_, _ = cli.ImageRemove(
			context.WithoutCancel(t.Context()),
			ref,
			client.ImageRemoveOptions{Force: true},
		)
	})
	return ref
}

// probe builds testdata/probe, statically, for the daemon's architecture.
func probe(t *testing.T, cli *client.Client) string {
	t.Helper()
	info, err := cli.Info(t.Context(), client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if arch == "" {
		t.Fatalf("no GOARCH for the daemon's architecture %q", info.Info.Architecture)
	}
	bin := filepath.Join(t.TempDir(), "infraweave")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./testdata/probe")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the probe: %v: %s", err, out)
	}
	return bin
}
