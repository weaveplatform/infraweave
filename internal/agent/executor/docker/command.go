package docker

import (
	"context"
	"errors"
	"fmt"
	"io"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
)

var _ executor.Commander = (*Executor)(nil)

// Command runs c's helper: a container of c.Image, pulled as a run's is,
// whose root process is the launcher's binary, mounted read-only at
// BinaryPath, run as runner command, as User, with every capability
// dropped, no new privileges, bounded memory and processes, no
// environment and no other mount, on the runs' network and runtime. Its
// standard input takes c.Request and closes, so the arguments never
// reach the container's configuration; its standard output goes to
// c.Output. The container is removed however the call ends.
//
// ref: decision 0009 § "The worker-side initialization policy"
func (e *Executor) Command(ctx context.Context, c executor.Command) (err error) {
	if err := e.image(ctx, c.Image); err != nil {
		return err
	}
	pids := int64(commandPids)
	created, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:        c.Image,
			User:         User,
			Entrypoint:   []string{BinaryPath},
			Cmd:          []string{"runner", "command"},
			Labels:       map[string]string{LabelCommandWorker: c.Worker, LabelCommandRun: c.Run},
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			OpenStdin:    true,
			StdinOnce:    true,
		},
		HostConfig: &container.HostConfig{
			Binds:       []string{e.binary + ":" + BinaryPath + ":ro"},
			NetworkMode: container.NetworkMode(e.c.Network),
			Runtime:     e.c.Runtime,
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
			Resources:   container.Resources{Memory: commandMemory, PidsLimit: &pids},
		},
	})
	if err != nil {
		return fmt.Errorf("docker: create run %s's initialization command: %w", c.Run, err)
	}
	defer func() {
		_, rerr := e.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true})
		if rerr != nil && !cerrdefs.IsNotFound(rerr) {
			err = errors.Join(err, fmt.Errorf("docker: remove run %s's initialization command: %w", c.Run, rerr))
		}
	}()
	att, err := e.cli.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		return fmt.Errorf("docker: attach to run %s's initialization command: %w", c.Run, err)
	}
	defer att.Close()
	if _, err := e.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("docker: start run %s's initialization command: %w", c.Run, err)
	}
	if _, err := att.Conn.Write(c.Request); err != nil {
		return fmt.Errorf("docker: send run %s's initialization command: %w", c.Run, err)
	}
	if err := att.CloseWrite(); err != nil {
		return fmt.Errorf("docker: send run %s's initialization command: %w", c.Run, err)
	}
	out := c.Output
	if out == nil {
		out = io.Discard
	}
	var copyErr error
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		// The helper writes nothing to standard error but its own failure,
		// which its missing result already reports.
		_, copyErr = stdcopy.StdCopy(out, io.Discard, att.Reader)
	}()
	select {
	case <-copied:
		if copyErr != nil {
			return fmt.Errorf("docker: read run %s's initialization command: %w", c.Run, copyErr)
		}
		return nil
	case <-ctx.Done():
		// Closing the stream ends the copy; the context is why.
		att.Close()
		<-copied
		return fmt.Errorf("docker: run %s's initialization command: %w", c.Run, context.Cause(ctx))
	}
}
