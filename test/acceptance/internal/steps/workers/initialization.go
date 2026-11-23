package workers

import (
	"context"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func (l *launcherSteps) initializationMasked(name string) error {
	launcher := l.launchers[name]
	if launcher == nil || launcher.srv == nil {
		return fmt.Errorf("%w: launcher unavailable", harness.ErrStep)
	}
	logs := string(launcher.srv.Stdout())
	if !strings.Contains(logs, "worker-policy-checked") || !strings.Contains(logs, "*****") {
		return fmt.Errorf("%w: masked policy print absent", harness.ErrStep)
	}
	if strings.Contains(logs, "eyJ") || strings.Contains(string(launcher.srv.Stderr()), "eyJ") {
		return fmt.Errorf("%w: JWT material in launcher diagnostics", harness.ErrStep)
	}
	for _, call := range l.relay.Calls() {
		if !strings.HasSuffix(call.Path, "/reports") {
			continue
		}
		// The relay's ordinary call receipt has no request body or token.
		if call.Status < 200 || call.Status >= 300 {
			return fmt.Errorf("%w: policy report refused", harness.ErrStep)
		}
	}
	return nil
}

func (l *launcherSteps) noRunContainer(ctx context.Context, run string) error {
	id := l.w.IDs[run]
	if err := l.noContainer(ctx, run); err != nil {
		return err
	}
	for _, call := range l.relay.Calls() {
		if call.Path == "/worker/v1/runs/"+id+"/reports" && call.Status >= 200 && call.Status < 300 {
			return nil
		}
	}
	return fmt.Errorf("%w: ordinary failure report absent", harness.ErrStep)
}

// commandRuns waits until an initialization command helper of run runs
// on the launchers' Docker host.
func (l *launcherSteps) commandRuns(ctx context.Context, run string) error {
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "an initialization command of "+run+" to run", func(ctx context.Context) (bool, error) {
		_, running, err := host.CommandContainers(ctx, l.w.IDs[run])
		return running > 0, err
	})
}

// commandLeft checks that a helper of run is still on the host, as a
// launcher killed mid-call leaves it.
func (l *launcherSteps) commandLeft(ctx context.Context, run string) error {
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	total, _, err := host.CommandContainers(ctx, l.w.IDs[run])
	if err != nil {
		return err
	}
	if total == 0 {
		return fmt.Errorf("%w: the run %s has no initialization command container", harness.ErrStep, run)
	}
	return nil
}

// noCommand waits until no initialization command helper of run, running
// or stopped, is left on the host.
func (l *launcherSteps) noCommand(ctx context.Context, run string) error {
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the initialization commands of "+run+" to be removed", func(ctx context.Context) (bool, error) {
		total, _, err := host.CommandContainers(ctx, l.w.IDs[run])
		return total == 0, err
	})
}

// noContainer checks that no run container of run exists on the host,
// as for a run whose initialization never admitted it.
func (l *launcherSteps) noContainer(ctx context.Context, run string) error {
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	count, err := host.RunContainerCount(ctx, l.w.IDs[run])
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w: the run %s has %d containers", harness.ErrStep, run, count)
	}
	return nil
}
