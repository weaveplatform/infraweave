package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"

	"github.com/weaveplatform/infraweave/internal/platform/runnerimages"
)

// Released immutable environments, shared with product defaults.
const (
	RunnerTerraform = runnerimages.Terraform
	RunnerOpenTofu  = runnerimages.OpenTofu
)

var runnerReferences = map[string]bool{
	RunnerTerraform: true, RunnerOpenTofu: true,
	runnerimages.Pulumi: true, runnerimages.Ansible: true, RunnerImage: true,
}

// contractPath is where RunnerContract mounts deploy/images/contract.sh.
const contractPath = "/opt/contract.sh"

// Errors of the runner images.
var (
	// ErrRunnerImage is returned for an image outside the released catalogue.
	ErrRunnerImage = errors.New("no released runner image")
	// ErrRunnerContract is returned for an image that breaks the runner
	// image contract.
	ErrRunnerContract = errors.New("the runner image contract")
)

// EnsureRunnerImage pulls a missing released image. It never builds or
// retags an image and never removes one owned by another suite. Failed
// pulls are retryable; only successful preparation is memoized.
func (l *Launchers) EnsureRunnerImage(ctx context.Context, image string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !runnerReferences[image] {
		return fmt.Errorf("%w: %q", ErrRunnerImage, image)
	}
	if l.ready[image] {
		return nil
	}
	if err := pullRunnerImage(ctx, l.cli, image); err != nil {
		return err
	}
	if l.ready == nil {
		l.ready = map[string]bool{}
	}
	l.ready[image] = true
	return nil
}

func pullRunnerImage(ctx context.Context, cli *testcontainers.DockerClient, image string) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pull runner image %s: %w", image, err)
		}
		retry, err := pullRunnerImageAttempt(ctx, cli, image)
		if stopped := ctx.Err(); stopped != nil {
			return fmt.Errorf("pull runner image %s: %w", image, stopped)
		}
		if err == nil || !retry || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("pull runner image %s: %w", image, ctx.Err())
		case <-timer.C:
		}
	}
}

func pullRunnerImageAttempt(ctx context.Context, cli *testcontainers.DockerClient, image string) (bool, error) {
	if _, err := cli.ImageInspect(ctx, image); err == nil {
		return false, nil
	} else if !cerrdefs.IsNotFound(err) {
		return false, fmt.Errorf("inspect runner image %s: %w", image, err)
	}
	pull, err := cli.ImagePull(ctx, image, client.ImagePullOptions{})
	if err != nil {
		return runnerPullReset(err), fmt.Errorf("pull runner image %s: %w", image, err)
	}
	if err := pull.Wait(ctx); err != nil {
		return runnerPullReset(err), fmt.Errorf("pull runner image %s: %w", image, err)
	}
	return false, nil
}

func runnerPullReset(err error) bool {
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	// The daemon serializes registry transport errors, losing net.OpError.
	// Other daemon errors, including coded auth and digest failures, stay terminal.
	var remote *jsonstream.Error
	return errors.As(err, &remote) && remote.Code == 0 && strings.HasSuffix(remote.Message, ": read: connection reset by peer")
}

// RunnerContract pulls the released image and runs deploy/images/contract.sh in a
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
	if err := pullRunnerImage(ctx, cli, image); err != nil {
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
