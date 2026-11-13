package state

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// stateLockTimeout is the lock timeout a scenario's runner is handed,
// launcher.state_lock_timeout's default.
const stateLockTimeout = 5 * time.Minute

// runnerEnv is the runner's container environment: no update check, and
// a home directory the image has. The runner sets the tool's automation
// switches itself.
var runnerEnv = []string{"CHECKPOINT_DISABLE=1", "HOME=/tmp"}

// registerRunner registers the steps that run this build's runner as a
// launcher starts it.
func (b *backendSteps) registerRunner(sc *godog.ScenarioContext) {
	sc.Step(`^the runner executes the run "(\w+)" with (terraform|tofu) and exits (\d+)$`,
		b.runner)
	sc.Step(`^the runner's output of the run "(\w+)" holds "([^"]*)"$`, b.holds)
}

// runner runs this build's runner for the run kept as run, as a launcher
// starts it: in a container of name's runner image, the run's workspace
// mounted, name's pinned binary at /bin/name, and the run's handoff on
// its standard input. It waits for the runner to exit and checks its
// exit code against code.
//
// ref: decision 0008 § "The launcher and the runner"
func (b *backendSteps) runner(ctx context.Context, run, name string, code int) error {
	pinned := tools[name]
	if !slices.Contains(b.w.Tags, pinned.tag) {
		return fmt.Errorf("%w: a scenario that runs %s needs %s", harness.ErrStep, name,
			pinned.tag)
	}
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	bin, err := b.w.Suite.Deps.Tool(ctx, pinned.tool, b.host.Arch)
	if err != nil {
		return err //nolint:wrapcheck // names the tool
	}
	if err := b.host.Build(ctx, pinned.image); err != nil {
		return err //nolint:wrapcheck // names the image
	}
	var in bytes.Buffer
	if err := handoff.Write(&in, tr.handoff); err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	t, err := b.host.RunTool(ctx, harness.ToolRun{
		Scenario: b.label, Network: b.network, Bin: b.host.Runner, Dir: tr.dir,
		Image: pinned.image, Env: runnerEnv, Args: []string{"runner"},
		Mounts: map[string]string{bin: "/bin/" + name}, Stdin: in.Bytes(),
	})
	if err != nil {
		return err
	}
	return b.wait(ctx, &process{tool: t, run: tr, what: "the runner with " + name}, code)
}

// colours matches the SGR escape sequences the tools colour their output
// with.
var colours = regexp.MustCompile("\x1b\\[[0-9;]*m")

// holds checks that the last standard output of the run kept as run's
// runner holds want, once its colours are stripped.
func (b *backendSteps) holds(run, want string) error {
	tr, err := b.run(run)
	if err != nil {
		return err
	}
	if out := colours.ReplaceAllString(tr.stdout, ""); !strings.Contains(out, want) {
		return fmt.Errorf("%w: the output is %q", harness.ErrStep, out)
	}
	return nil
}
