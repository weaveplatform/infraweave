package harness

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/test/deps"
)

func TestAccRunnerImageOwnerCannotRemovePeerImage(t *testing.T) {
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(EnvRequireDocker) == "1" {
			t.Fatal("required real Docker daemon unavailable", err)
		}
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	owners := make([]*Launchers, 0, 2)
	for range 2 {
		cli, err := testcontainers.NewDockerClientWithOpts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		owner := &Launchers{cli: cli}
		owners = append(owners, owner)
		t.Cleanup(func() {
			if err := owner.Close(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, cerrdefs.ErrNotFound) {
				t.Error("owned image cleanup", err)
			}
		})
		if err := importRunnerImage(ctx, cli); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := owners[1].cli.ImageInspect(ctx, RunnerImage); err != nil {
		t.Fatal("peer image was not imported before the lifecycle barrier", err)
	}
	// Close returning is the barrier: B's image is already imported, A has
	// completed its own cleanup, and only then does B need its next container.
	if err := owners[0].Close(ctx); err != nil {
		t.Fatal("first owner's close", err)
	}
	if _, err := owners[1].cli.ImageInspect(ctx, RunnerImage); err != nil {
		t.Fatal("closing owner A removed owner B's real Docker image", err)
	}
}
