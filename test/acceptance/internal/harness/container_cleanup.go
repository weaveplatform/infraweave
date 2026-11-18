package harness

import (
	"context"
	"errors"
	"fmt"
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
	host := &container.HostConfig{VolumesFrom: []string{id}, NetworkMode: "none"}
	if in.Container.HostConfig != nil {
		host.Runtime = in.Container.HostConfig.Runtime
	}
	helper, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image: in.Container.Image, User: docker.User, WorkingDir: docker.Workspace,
			Entrypoint: []string{docker.BinaryPath}, Cmd: []string{"runner", "hand-back"},
		},
		HostConfig: host,
	})
	if err != nil {
		// A launcher may finish removing its own run between inspection and
		// VolumesFrom creation. Missing images still remain genuine failures.
		if cerrdefs.IsNotFound(err) {
			if _, current := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{}); cerrdefs.IsNotFound(current) {
				return nil
			}
		}
		return fmt.Errorf("create scenario hand-back: %w", err)
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
