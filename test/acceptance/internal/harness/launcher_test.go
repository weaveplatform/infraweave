package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/errdefs"
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
	previousBody, err := os.ReadFile(filepath.Join(root, "test/fixtures/runner-images/previous.json"))
	if err != nil {
		t.Fatal(err)
	}
	var previous struct {
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal(previousBody, &previous); err != nil {
		t.Fatal(err)
	}
	if previous.Reference == RunnerTerraform || !strings.HasPrefix(previous.Reference,
		"ghcr.io/weaveplatform/infraweave-runner-terraform@sha256:") {
		t.Fatal("rollback fixture must name a distinct retained Terraform index")
	}
	prefix := filepath.Base(filepath.Dir(t.TempDir()))
	var a, b Launchers
	for _, owner := range []*Launchers{&a, &b} {
		t.Cleanup(func() { _ = owner.Close(context.WithoutCancel(ctx)) })
		if err := owner.prepare(ctx, root, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if err := owner.EnsureRunnerImage(ctx, RunnerTerraform); err != nil {
			t.Fatal(err)
		}
	}
	first, err := b.cli.ImageInspect(ctx, RunnerImage)
	if err != nil {
		t.Fatal(err)
	}
	sharedOwnerBaseline(t, &a, &b, prefix)
	startForward := func(owner *Launchers, scenario string) {
		t.Helper()
		t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, scenario) })
		_, err := owner.RunTool(ctx, ToolRun{
			Scenario: scenario, Bin: owner.Forward, Dir: t.TempDir(),
			Args: []string{"127.0.0.1:0", "127.0.0.1:1"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	startForward(&a, prefix+"-a")
	startForward(&b, prefix+"-b")
	sharedOwnerFailureCleanup(t, &a, &b, root, prefix)
	if err := pullRunnerImage(ctx, b.cli, previous.Reference); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx, LabelScenario, prefix+"-a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(ctx); err != nil {
		t.Fatalf("repeated owner cleanup: %v", err)
	}
	startForward(&b, prefix+"-b-next")
	sharedOwnerPlan(t, &b, prefix+"-plan")
	t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, prefix+"-version") })
	for _, image := range []string{RunnerImage, previous.Reference, RunnerTerraform, previous.Reference, RunnerOpenTofu} {
		var err error
		if image == previous.Reference {
			err = pullRunnerImage(ctx, b.cli, image)
		} else {
			err = b.EnsureRunnerImage(ctx, image)
		}
		if err != nil {
			t.Fatal(err)
		}
		tool, err := b.RunTool(ctx, ToolRun{
			Scenario: prefix + "-version", Bin: b.Runner, Dir: t.TempDir(),
			Image: image, Args: []string{"version"},
		})
		if err != nil {
			t.Fatal(err)
		}
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
	for _, suffix := range []string{"-a", "-b", "-b-next", "-plan", "-version"} {
		scenario := prefix + suffix
		if err := b.Remove(ctx, LabelScenario, scenario); err != nil {
			t.Fatal(err)
		}
		list, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
			All: true, Filters: client.Filters{}.Add("label", LabelScenario+"="+scenario),
		})
		if err != nil || len(list.Items) != 0 {
			t.Fatalf("owned container cleanup readback: %v, %v", list.Items, err)
		}
	}
}

func sharedOwnerBaseline(t *testing.T, a, b *Launchers, prefix string) {
	t.Helper()
	ctx := t.Context()
	// A private alias recreates the rejected shared-tag teardown without
	// removing any committed reference another suite may be using.
	alias := "infraweave-ownership-control:" + strings.ToLower(prefix)
	if _, err := b.cli.ImageInspect(ctx, alias); !errdefs.IsNotFound(err) {
		t.Fatalf("baseline alias must be absent before ownership: %v", err)
	}
	if _, err := a.cli.ImageTag(ctx, client.ImageTagOptions{Source: RunnerImage, Target: alias}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := b.cli.ImageRemove(context.WithoutCancel(ctx), alias, client.ImageRemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			t.Errorf("baseline alias cleanup: %v", err)
		}
	})
	var surviving *Tool
	for _, owner := range []struct {
		launcher *Launchers
		scenario string
	}{{a, prefix + "-baseline-a"}, {b, prefix + "-baseline-b"}} {
		t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, owner.scenario) })
		tool, err := owner.launcher.RunTool(ctx, ToolRun{
			Scenario: owner.scenario, Image: alias, Bin: owner.launcher.Forward, Dir: t.TempDir(),
			Args: []string{"127.0.0.1:0", "127.0.0.1:1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		surviving = tool
	}
	if err := a.Remove(ctx, LabelScenario, prefix+"-baseline-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.cli.ImageRemove(ctx, alias, client.ImageRemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.cli.ImageInspect(ctx, alias); !errdefs.IsNotFound(err) {
		t.Fatalf("baseline teardown retained the shared alias: %v", err)
	}
	live, err := b.cli.ContainerInspect(ctx, surviving.id, client.ContainerInspectOptions{})
	if err != nil || live.Container.State == nil || !live.Container.State.Running {
		t.Fatalf("the barrier must leave B's first tool running: %v, %v", live.Container, err)
	}
	scenario := prefix + "-baseline-next"
	t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, scenario) })
	if _, err := b.RunTool(ctx, ToolRun{
		Scenario: scenario, Image: alias, Bin: b.Forward, Dir: t.TempDir(),
		Args: []string{"127.0.0.1:0", "127.0.0.1:1"},
	}); !errdefs.IsNotFound(err) {
		t.Fatalf("shared-tag baseline must fail at B's next create: %v", err)
	} else {
		t.Logf("Controlled shared-tag baseline refused B's next tool after A removed its alias: %v", err)
	}
	for _, suffix := range []string{"-baseline-a", "-baseline-b", "-baseline-next"} {
		scenario := prefix + suffix
		if err := b.Remove(ctx, LabelScenario, scenario); err != nil {
			t.Fatal(err)
		}
		remaining, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
			All: true, Filters: client.Filters{}.Add("label", LabelScenario+"="+scenario),
		})
		if err != nil || len(remaining.Items) != 0 {
			t.Fatalf("baseline container cleanup readback: %v, %v", remaining.Items, err)
		}
	}
}

func sharedOwnerFailureCleanup(t *testing.T, a, b *Launchers, root, prefix string) {
	t.Helper()
	ctx := t.Context()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	var partial Launchers
	t.Cleanup(func() { _ = partial.Close(context.WithoutCancel(ctx)) })
	if err := partial.prepare(canceled, root, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation: %v", err)
	}
	if err := partial.Close(ctx); err != nil {
		t.Fatalf("partial preparation cleanup: %v", err)
	}

	broken := filepath.Join(t.TempDir(), "invalid-executable")
	if err := os.WriteFile(broken, []byte("invalid executable\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	scenario := prefix + "-failed-start"
	t.Cleanup(func() { _ = b.Remove(context.WithoutCancel(ctx), LabelScenario, scenario) })
	failedTool, startErr := a.RunTool(ctx, ToolRun{Scenario: scenario, Bin: broken, Dir: t.TempDir()})
	diagnostic := ""
	if startErr != nil {
		diagnostic = startErr.Error()
	} else {
		// Some runtimes acknowledge start before reporting the exec failure.
		code, stdout, stderr, err := failedTool.Wait(ctx)
		if err != nil || code == 0 {
			t.Fatalf("invalid executable: code=%d stdout=%q stderr=%q err=%v", code, stdout, stderr, err)
		}
		diagnostic = stdout + stderr
	}
	failed, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true, Filters: client.Filters{}.Add("label", LabelScenario+"="+scenario),
	})
	if err != nil || len(failed.Items) != 1 {
		t.Fatalf("failed startup must retain one labelled container for teardown: %v, %v", failed.Items, err)
	}
	inspect, err := b.cli.ContainerInspect(ctx, failed.Items[0].ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Container.State == nil || diagnostic == "" && inspect.Container.State.Error == "" {
		t.Fatalf("failed startup lost Docker's diagnostic: %v", inspect.Container.State)
	}
	if err := a.Remove(context.WithoutCancel(canceled), LabelScenario, scenario); err != nil {
		t.Fatalf("failed scenario cleanup: %v", err)
	}

	live, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
		Filters: client.Filters{}.Add("label", LabelScenario+"="+prefix+"-a"),
	})
	if err != nil || len(live.Items) != 1 {
		t.Fatalf("cancellation control needs A's running tool: %v, %v", live.Items, err)
	}
	tool := &Tool{cli: a.cli, id: live.Items[0].ID}
	if _, _, _, err := tool.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait: %v", err)
	}
	if err := a.Remove(context.WithoutCancel(canceled), LabelScenario, prefix+"-a"); err != nil {
		t.Fatalf("canceled scenario cleanup: %v", err)
	}
	for _, label := range []string{scenario, prefix + "-a"} {
		remaining, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
			All: true, Filters: client.Filters{}.Add("label", LabelScenario+"="+label),
		})
		if err != nil || len(remaining.Items) != 0 {
			t.Fatalf("failed/canceled scenario cleanup readback: %v, %v", remaining.Items, err)
		}
	}
	remaining, err := b.cli.ContainerList(ctx, client.ContainerListOptions{
		Filters: client.Filters{}.Add("label", LabelScenario+"="+prefix+"-b"),
	})
	if err != nil || len(remaining.Items) != 1 {
		t.Fatalf("failure cleanup removed B's running tool: %v, %v", remaining.Items, err)
	}
	t.Log("Canceled preparation, failed startup and canceled wait cleaned up; B's running container survived.")
}

func sharedOwnerPlan(t *testing.T, owner *Launchers, scenario string) {
	t.Helper()
	ctx := t.Context()
	d := &deps.Dependencies{}
	t.Cleanup(func() { _ = d.Close(context.WithoutCancel(ctx)) })
	bin, err := d.Tool(ctx, deps.Terraform, owner.Arch)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.tf"),
		[]byte("resource \"terraform_data\" \"survives\" { input = \"shared-image-survives\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Remove(context.WithoutCancel(ctx), LabelScenario, scenario) })
	for _, args := range [][]string{
		{"init", "-input=false", "-no-color"},
		{"plan", "-input=false", "-no-color", "-out=proof.plan"},
	} {
		tool, err := owner.RunTool(ctx, ToolRun{
			Scenario: scenario, Bin: bin, Dir: workspace, Image: RunnerTerraform, Args: args,
			Env: []string{"HOME=/tmp", "TF_IN_AUTOMATION=1", "CHECKPOINT_DISABLE=1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr, err := tool.Wait(ctx)
		if err != nil || code != 0 || args[0] == "plan" && !strings.Contains(stdout, "Plan: 1 to add") {
			t.Fatalf("post-cleanup Terraform %v: code=%d stdout=%q stderr=%q err=%v", args, code, stdout, stderr, err)
		}
		inspect, err := owner.cli.ContainerInspect(ctx, tool.id, client.ContainerInspectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		readOnly := false
		for _, mount := range inspect.Container.Mounts {
			if mount.Destination == toolPath && !mount.RW {
				readOnly = true
			}
		}
		if !readOnly || inspect.Container.Config.Image != RunnerTerraform {
			t.Fatal("the post-cleanup plan must use the committed image and read-only tool mount")
		}
	}
}
