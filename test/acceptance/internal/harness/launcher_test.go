package harness

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/test/deps"
)

// TestAccSharedImageLifecycle keeps both owners' real containers alive,
// closes A, then starts B's next forwarder and tool. Removing a shared
// image at A's teardown makes the next create fail with NoSuchImage.
func TestAccSharedImageLifecycle(t *testing.T) {
	ctx := t.Context()
	if err := deps.DockerReachable(ctx); err != nil {
		if os.Getenv(EnvRequireDocker) == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Base(filepath.Dir(t.TempDir()))
	var a, b Launchers
	for _, owner := range []*Launchers{&a, &b} {
		if err := owner.prepare(ctx, root, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = owner.Close(context.WithoutCancel(ctx)) })
		if err := owner.EnsureRunnerImage(ctx, RunnerTerraform); err != nil {
			t.Fatal(err)
		}
	}
	first, err := b.cli.ImageInspect(ctx, RunnerImage)
	if err != nil {
		t.Fatal(err)
	}
	startForward := func(owner *Launchers, scenario string) {
		t.Helper()
		_, err := owner.RunTool(ctx, ToolRun{
			Scenario: scenario, Bin: owner.Forward, Dir: t.TempDir(),
			Args: []string{"127.0.0.1:0", "127.0.0.1:1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, scenario) })
	}
	startForward(&a, prefix+"-a")
	startForward(&b, prefix+"-b")
	if err := a.Remove(ctx, LabelScenario, prefix+"-a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	startForward(&b, prefix+"-b-next")
	for _, image := range []string{RunnerImage, RunnerTerraform, RunnerOpenTofu, RunnerTerraform} {
		if err := b.EnsureRunnerImage(ctx, image); err != nil {
			t.Fatal(err)
		}
		tool, err := b.RunTool(ctx, ToolRun{
			Scenario: prefix + "-version", Bin: b.Runner, Dir: t.TempDir(),
			Image: image, Args: []string{"version"},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, prefix+"-version") })
		code, stdout, stderr, err := tool.Wait(ctx)
		if err != nil || code != 0 || !strings.Contains(stdout, "infraweave") {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q err=%v", image, code, stdout, stderr, err)
		}
		got, err := b.cli.ContainerInspect(ctx, tool.id, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Container.Config.Image != image {
			t.Fatalf("warm-cache selection/rollback: %v", got.Container.Config)
		}
	}
	last, err := b.cli.ImageInspect(ctx, RunnerImage)
	if err != nil || last.ID != first.ID {
		t.Fatalf("shared cached image changed across owners: %v, %v", last.ID, err)
	}
}
