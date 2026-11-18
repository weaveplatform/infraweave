package harness

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
)

type cleanupDocker interface {
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerKill(context.Context, string, client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerWait(context.Context, string, client.ContainerWaitOptions) client.ContainerWaitResult
	ContainerInspect(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerCreate(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
}

func removeContainers(ctx context.Context, cli cleanupDocker, key, value string) error {
	list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", key+"="+value)})
	if err != nil {
		return fmt.Errorf("docker: %w", err)
	}
	var errs error
	for _, c := range list.Items {
		if c.Labels[docker.LabelWorker] != "" && c.Labels[docker.LabelRun] != "" {
			if err := handBackContainer(ctx, cli, c.ID); err != nil {
				errs = errors.Join(errs, err)
				continue
			}
		}
		_, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
		if !cerrdefs.IsConflict(err) && !cerrdefs.IsNotFound(err) {
			errs = errors.Join(errs, err)
		}
	}
	if errs != nil {
		return fmt.Errorf("docker: %w", errs)
	}
	return nil
}

// A stopped launcher deliberately leaves its runner alive for restart. End it
// before handing back its UID-owned directories, retaining the original mounts
// until the existing runner hand-back protocol has completed successfully.
func handBackContainer(ctx context.Context, cli cleanupDocker, id string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := cli.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: "SIGKILL"}); cerrdefs.IsNotFound(err) {
		return nil
	} else if err != nil && !cerrdefs.IsConflict(err) {
		return fmt.Errorf("stop scenario run: %w", err)
	}
	if err := waitCleanupContainer(ctx, cli, id, false); cerrdefs.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	in, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect scenario run: %w", err)
	}
	helper, err := createCleanupContainer(ctx, cli, id, in.Container)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, removed := cli.ContainerRemove(cleanup, helper.ID, client.ContainerRemoveOptions{Force: true})
		if !cerrdefs.IsConflict(removed) && !cerrdefs.IsNotFound(removed) {
			err = errors.Join(err, removed)
		}
	}()
	if _, err := cli.ContainerStart(ctx, helper.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start scenario hand-back: %w", err)
	}
	return waitCleanupContainer(ctx, cli, helper.ID, true)
}

func createCleanupContainer(ctx context.Context, cli cleanupDocker, id string, in container.InspectResponse) (client.ContainerCreateResult, error) {
	host := &container.HostConfig{VolumesFrom: []string{id}, NetworkMode: "none"}
	if in.HostConfig != nil {
		host.Runtime = in.HostConfig.Runtime
	}
	options := client.ContainerCreateOptions{
		Config: &container.Config{
			Image: in.Image, User: docker.User, WorkingDir: docker.Workspace,
			Entrypoint: []string{docker.BinaryPath}, Cmd: []string{"runner", "hand-back"},
		},
		HostConfig: host,
	}
	helper, err := cli.ContainerCreate(ctx, options)
	if err == nil {
		return helper, nil
	}
	original := fmt.Errorf("create scenario hand-back: %w", err)
	// Moby reports a missing VolumesFrom source as an invalid parameter.
	// Confirm its disappearance separately; neither an error message nor a
	// failed create proves that the workspace's permissions were handed back.
	if !cerrdefs.IsInvalidArgument(err) && !cerrdefs.IsNotFound(err) {
		return helper, original
	}
	_, current := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if !cerrdefs.IsNotFound(current) {
		if current != nil {
			return helper, errors.Join(original, fmt.Errorf("reinspect scenario run: %w", current))
		}
		return helper, original
	}
	if in.HostConfig == nil || len(in.HostConfig.Binds) == 0 {
		return helper, original
	}
	// Owned run containers mount the runner, workspace and tools through
	// explicit binds. Retaining those exact binds lets hand-back finish even
	// after the launcher's concurrent removal discards the source container.
	host.VolumesFrom = nil
	host.Binds = slices.Clone(in.HostConfig.Binds)
	helper, err = cli.ContainerCreate(ctx, options)
	if err != nil {
		return helper, errors.Join(original, fmt.Errorf("create retained scenario hand-back: %w", err))
	}
	return helper, nil
}

func waitCleanupContainer(ctx context.Context, cli cleanupDocker, id string, requireSuccess bool) error {
	wait := cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case result := <-wait.Result:
		if result.Error != nil {
			return fmt.Errorf("scenario container wait: %s", result.Error.Message)
		}
		if requireSuccess && result.StatusCode != 0 {
			return fmt.Errorf("scenario container exited %d", result.StatusCode)
		}
		return nil
	case err := <-wait.Error:
		return err //nolint:wrapcheck // the caller distinguishes an already removed container
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the bounded caller names this cleanup
	}
}
