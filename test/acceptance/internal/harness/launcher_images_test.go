package harness

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/test/deps"
)

func realImageOwner(t *testing.T, ctx context.Context) *Launchers {
	t.Helper()
	if err := deps.DockerReachable(ctx); err != nil {
		if os.Getenv(EnvRequireDocker) == "1" {
			t.Fatal("required real Docker daemon unavailable", err)
		}
		t.Skip(err)
	}
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := &Launchers{cli: cli}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := owner.Close(cleanup); err != nil {
			t.Error("owned image cleanup", err)
		}
	})
	return owner
}

func TestAccRunnerImageOwnerCannotRemovePeerImage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	owners := make([]*Launchers, 0, 2)
	for range 2 {
		owner := realImageOwner(t, ctx)
		owners = append(owners, owner)
		if err := owner.importRunnerImage(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if owners[0].Image == owners[1].Image {
		t.Fatal("independent owners share a mutable image reference", owners[0].Image)
	}
	if _, err := owners[1].cli.ImageInspect(ctx, owners[1].Image); err != nil {
		t.Fatal("peer image was not imported before the lifecycle barrier", err)
	}
	// Close returning is the barrier: B's image is already imported, A has
	// completed its own cleanup, and only then does B need its next container.
	if err := owners[0].Close(ctx); err != nil {
		t.Fatal("first owner's close", err)
	}
	if _, err := owners[1].cli.ImageInspect(ctx, owners[1].Image); err != nil {
		t.Fatal("closing owner A removed owner B's real Docker image", err)
	}
	if _, err := owners[1].cli.ImageInspect(ctx, owners[0].Image); !cerrdefs.IsNotFound(err) {
		t.Fatal("owner A's image was not removed", err)
	}
	if err := owners[1].Close(ctx); err != nil {
		t.Fatal("owner B's close", err)
	}
	if _, err := owners[1].cli.ImageInspect(ctx, owners[1].Image); !cerrdefs.IsNotFound(err) {
		t.Fatal("owner B's image remained after cleanup", err)
	}
}

func TestAccRunnerImageCleanupPreservesSharedContentAndCustomTag(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	a, b := realImageOwner(t, ctx), realImageOwner(t, ctx)
	if err := a.importRunnerImage(ctx); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := b.importRunnerImage(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled import concealed its cause", err)
	}
	custom := "infraweave-runner:custom-" + strings.ToLower(rand.Text())
	for _, target := range []string{b.Image, custom} {
		if _, err := a.cli.ImageTag(ctx, client.ImageTagOptions{Source: a.Image, Target: target}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if _, err := b.cli.ImageRemove(context.WithoutCancel(ctx), custom, client.ImageRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
			t.Error("test-owned custom tag cleanup", err)
		}
	})
	first, err := a.cli.ImageInspect(ctx, a.Image)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.cli.ImageInspect(ctx, b.Image)
	if err != nil || first.ID != second.ID || first.Config == nil || first.Config.User != "runner" || first.Config.WorkingDir != workspacePath {
		t.Fatal("fixture did not share the real image content or retained contract", first, second, err)
	}
	for range 2 {
		if err := a.Close(ctx); err != nil {
			t.Fatal("repeated owner close failed", err)
		}
	}
	if _, err := b.cli.ImageInspect(ctx, b.Image); err != nil {
		t.Fatal("untagging A removed B's shared image", err)
	}
	if err := b.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cleanup hid its cause", err)
	}
	if _, err := b.cli.ImageInspect(ctx, b.Image); err != nil {
		t.Fatal("canceled cleanup removed the peer before retry", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal("fresh cleanup could not retry", err)
	}
	if _, err := b.cli.ImageInspect(ctx, b.Image); !cerrdefs.IsNotFound(err) {
		t.Fatal("owner B's shared image reference remained after retry", err)
	}
	if _, err := b.cli.ImageInspect(ctx, custom); err != nil {
		t.Fatal("owner cleanup removed the custom reference", err)
	}
}

func TestAccRunnerImagePeerCloseKeepsForwarderAndToolFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	a, b := realImageOwner(t, ctx), realImageOwner(t, ctx)
	for _, owner := range []*Launchers{a, b} {
		if err := owner.importRunnerImage(ctx); err != nil {
			t.Fatal(err)
		}
	}
	info, err := b.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	root, err := filepath.Abs("../../../..")
	if err != nil || arch == "" {
		t.Fatal("real daemon build coordinates", root, arch, err)
	}
	dir := t.TempDir()
	probe, err := buildLinux(ctx, root, dir, arch, "./test/acceptance/testdata/image-owner-probe")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "linux" {
		b.Forward, err = buildLinux(ctx, root, dir, arch, "./test/acceptance/testdata/forward")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	label := "owner-probe-" + rand.Text()
	t.Cleanup(func() {
		if err := b.Remove(context.WithoutCancel(ctx), LabelScenario, label); err != nil {
			t.Error("owned probe container cleanup", err)
		}
	})
	network, err := b.Network(ctx, label, "23456")
	if err != nil {
		t.Fatal("peer cleanup broke the next native forwarder", err)
	}
	tool, err := b.RunTool(ctx, ToolRun{Scenario: label, Network: network, Bin: probe, Dir: t.TempDir()})
	if err != nil {
		t.Fatal("peer cleanup broke the root-image tool fallback", err)
	}
	code, stdout, stderr, err := tool.Wait(ctx)
	var report struct {
		UID int    `json:"uid"`
		Dir string `json:"dir"`
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(stdout), &report) != nil || report.UID != os.Getuid() || report.Dir != workspacePath {
		t.Fatal("actual tool changed user/workspace or failed", code, stdout, stderr, err)
	}
	if err := b.Remove(ctx, LabelScenario, label); err != nil {
		t.Fatal(err)
	}
	containers, err := b.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", LabelScenario+"="+label)})
	if err != nil || len(containers.Items) != 0 {
		t.Fatal("owned probe/forwarder survived cleanup", containers, err)
	}
}
