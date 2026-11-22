package workers

import (
	"context"
	"fmt"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
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
	host, err := l.w.Suite.Launchers(ctx)
	if err != nil {
		return err
	}
	count, err := host.RunContainerCount(ctx, id)
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w: denied run has %d containers", harness.ErrStep, count)
	}
	for _, call := range l.relay.Calls() {
		if call.Path == "/worker/v1/runs/"+id+"/reports" && call.Status >= 200 && call.Status < 300 {
			return nil
		}
	}
	return fmt.Errorf("%w: ordinary failure report absent", harness.ErrStep)
}
