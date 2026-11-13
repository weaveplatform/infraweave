package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// toolPath is where a tool's binary is mounted in its container, and
// workspacePath the workspace it runs in, RunnerImage's working
// directory.
const (
	toolPath      = "/opt/tool"
	workspacePath = "/mnt/workspace"
)

// ErrToolWait is returned when the daemon cannot say how a tool's
// container ended.
var ErrToolWait = errors.New("the tool's container did not end")

// Tool is one process of a tool, such as terraform or tofu, in a
// container of RunnerImage.
type Tool struct {
	l  *Launchers
	id string
}

// ToolRun is what a Tool runs: Bin, mounted read-only, with Args and Env
// in Dir, mounted as the workspace, on Network, labelled Scenario.
type ToolRun struct {
	Scenario, Network, Bin, Dir string
	Env, Args                   []string
}

// RunTool starts run's container. It runs as the host's user rather than
// the image's runner, so what the tool writes in the workspace its
// scenario reads and removes; on a Linux host the runner's UID would own
// it.
func (l *Launchers) RunTool(ctx context.Context, run ToolRun) (*Tool, error) {
	c, err := l.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:      RunnerImage,
			User:       strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
			Entrypoint: []string{toolPath},
			Cmd:        run.Args,
			Env:        run.Env,
			WorkingDir: workspacePath,
			Labels:     map[string]string{LabelScenario: run.Scenario},
		},
		HostConfig: &container.HostConfig{
			Binds: []string{
				run.Bin + ":" + toolPath + ":ro",
				run.Dir + ":" + workspacePath,
			},
			NetworkMode: container.NetworkMode(run.Network),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("the tool's container: %w", err)
	}
	if _, err := l.cli.ContainerStart(ctx, c.ID, client.ContainerStartOptions{}); err != nil {
		return nil, fmt.Errorf("the tool's container: %w", err)
	}
	return &Tool{l: l, id: c.ID}, nil
}

// Wait waits for the tool to exit, and answers its exit code and what it
// wrote to standard output and to standard error.
func (t *Tool) Wait(ctx context.Context) (code int, stdout, stderr string, err error) {
	w := t.l.cli.ContainerWait(ctx, t.id, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case res := <-w.Result:
		if res.Error != nil {
			return 0, "", "", fmt.Errorf("%w: %s", ErrToolWait, res.Error.Message)
		}
		code = int(res.StatusCode)
	case err := <-w.Error:
		return 0, "", "", fmt.Errorf("%w: %w", ErrToolWait, err)
	}
	logs, err := t.l.cli.ContainerLogs(ctx, t.id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true,
	})
	if err != nil {
		return 0, "", "", fmt.Errorf("the tool's output: %w", err)
	}
	defer logs.Close()
	var out, errOut bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errOut, logs); err != nil {
		return 0, "", "", fmt.Errorf("the tool's output: %w", err)
	}
	return code, out.String(), errOut.String(), nil
}
