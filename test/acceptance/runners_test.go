package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// TestAccRunnerImages builds each runner image the @terraform and @tofu
// scenarios run in from its definition under deploy/images, and holds a
// container of each to the runner image contract: as the image's own
// user, UID 1983, it runs ps and writes to its workspace, and finds no
// Terraform or OpenTofu binary.
func TestAccRunnerImages(t *testing.T) {
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(harness.EnvRequireDocker) == "1" {
			t.Fatalf("%s=1, and no Docker daemon answers: %v", harness.EnvRequireDocker, err)
		}
		t.Skipf("the runner images need Docker: %v", err)
	}
	root, err := filepath.Abs(harness.RepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{harness.RunnerTerraform, harness.RunnerOpenTofu} {
		t.Run(image, func(t *testing.T) {
			line, err := harness.RunnerContract(t.Context(), root, image)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(line, "runner image contract: met") {
				t.Errorf("the contract wrote %q", line)
			}
			t.Log(line)
		})
	}
}
