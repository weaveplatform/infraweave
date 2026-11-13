package harness

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"github.com/testcontainers/testcontainers-go"
)

// The runner images of @terraform and @tofu scenarios, built from their
// definitions under deploy/images, the ones CI builds and a release
// publishes. A scenario's tool runs in its vendor's, and a stack names
// its vendor's.
//
// ref: decision 0016
const (
	RunnerTerraform = "infraweave-runner-terraform:acceptance"
	RunnerOpenTofu  = "infraweave-runner-opentofu:acceptance"
)

// runnerDefinitions are the directories under deploy/images that define
// the runner images.
var runnerDefinitions = map[string]string{
	RunnerTerraform: "runner-terraform",
	RunnerOpenTofu:  "runner-opentofu",
}

// contractPath is where RunnerContract mounts deploy/images/contract.sh.
const contractPath = "/opt/contract.sh"

// Errors of the runner images.
var (
	// ErrRunnerImage is returned for an image no definition builds.
	ErrRunnerImage = errors.New("no runner image definition")
	// ErrRunnerContract is returned for an image that breaks the runner
	// image contract.
	ErrRunnerContract = errors.New("the runner image contract")
)

// Build builds image, RunnerTerraform or RunnerOpenTofu, once per test
// binary, on first use.
func (l *Launchers) Build(ctx context.Context, image string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err, ok := l.built[image]; ok {
		return err
	}
	if l.built == nil {
		l.built = map[string]error{}
	}
	l.built[image] = BuildRunner(ctx, l.cli, l.root, image)
	return l.built[image]
}

// BuildRunner builds image from its definition under root's deploy/images
// with the daemon's own builder, which needs no builder of its own. The
// suite leaves the image, so the next run's build of an unchanged
// definition reuses its layers rather than installing its packages again.
func BuildRunner(ctx context.Context, cli *testcontainers.DockerClient, root, image string) error {
	def, ok := runnerDefinitions[image]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRunnerImage, image)
	}
	var tarball bytes.Buffer
	tw := tar.NewWriter(&tarball)
	if err := tw.AddFS(os.DirFS(filepath.Join(root, "deploy", "images", def))); err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	res, err := cli.ImageBuild(ctx, &tarball, client.ImageBuildOptions{
		Tags: []string{image}, Remove: true, ForceRemove: true,
	})
	if err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	defer res.Body.Close()
	if err := jsonmessage.DisplayStream(res.Body, io.Discard); err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	return nil
}

// RunnerContract builds image and runs deploy/images/contract.sh in a
// container of it, as the image's own user and from its own working
// directory, as CI's images job does, and answers the line the script
// wrote. It refuses an image that breaks the contract as
// ErrRunnerContract, with what the script wrote.
func RunnerContract(ctx context.Context, root, image string) (string, error) {
	cli, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return "", fmt.Errorf("docker: %w", err)
	}
	defer cli.Close()
	if err := BuildRunner(ctx, cli, root, image); err != nil {
		return "", err
	}
	c, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{Image: image, Entrypoint: []string{"sh", contractPath}},
		HostConfig: &container.HostConfig{
			Binds: []string{
				filepath.Join(root, "deploy", "images", "contract.sh") + ":" + contractPath + ":ro",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("%s's contract: %w", image, err)
	}
	defer func() {
		_, _ = cli.ContainerRemove(context.WithoutCancel(ctx), c.ID,
			client.ContainerRemoveOptions{Force: true})
	}()
	if _, err := cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		return "", fmt.Errorf("%s's contract: %w", image, err)
	}
	code, stdout, stderr, err := (&Tool{cli: cli, id: c.ID}).Wait(ctx)
	switch {
	case err != nil:
		return "", err
	case code != 0:
		return "", fmt.Errorf("%w: %s exits %d: %s%s", ErrRunnerContract, image, code, stdout,
			stderr)
	}
	return strings.TrimSpace(stdout), nil
}
