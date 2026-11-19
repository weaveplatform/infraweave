package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// labSteps reach the lab a @vcs-lab scenario's @lab: tag names: the
// real provider and repository the vcs-lab job provisioned for the run
// and names through vcslab's variables. They run only in that job.
type labSteps struct {
	w *harness.World
}

func (l *labSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the vcs-lab lab's repository$`, l.repository)
	(&liveLabSteps{w: l.w}).register(sc)
}

// repository keeps the lab as {labHost}, {labUser}, {labToken},
// {labNamespace} and {labRepository}, and the commits seed.sh pushed to
// it as {labBase} and {labHead}. A lab whose variables are unset is not
// configured, which fails the scenario.
func (l *labSteps) repository() error {
	lab, err := vcslab.FromEnv(vcslab.EnvNamespace, vcslab.EnvRepository)
	if err != nil {
		return fmt.Errorf("%w: %w", harness.ErrStep, err)
	}
	if slices.Contains(l.w.Tags, "@lab:github") && lab.Host == "" {
		lab.Host = "https://api.github.com"
	}
	escaped, err := json.Marshal(lab.Token)
	if err != nil {
		return fmt.Errorf("encode lab credential: %w", err)
	}
	l.w.IDs["labKey"] = strings.Trim(string(escaped), `"`)
	for k, v := range map[string]string{
		"labHost": lab.Host, "labUser": lab.User, "labToken": lab.Token,
		"labNamespace": lab.Namespace, "labRepository": lab.Repository,
		"labBase": vcslab.Base, "labHead": vcslab.Head,
	} {
		l.w.IDs[k] = v
	}
	return nil
}

// VCS returns the settings and certificates a @vcs-lab scenario's
// servers need to reach its lab.
func (*labSteps) VCS(_ context.Context, w *harness.World) (map[string]any, []byte, error) {
	vcs := map[string]any{}
	ca, err := labVCS(w.Dir, w.Tags, vcs)
	return vcs, ca, err
}

// Stop has nothing to stop: the lab outlives the run.
func (*labSteps) Stop() {}

// labVCS adds to vcs the settings a @vcs-lab scenario's servers need to
// reach its lab, and returns the certificates they must trust beyond the
// system's. Every lab's servers trust its CA file when it names one;
// each scenario registers its account's integration through the API.
func labVCS(_ string, tags []string, _ map[string]any) ([]byte, error) {
	if !slices.Contains(tags, "@vcs-lab") {
		return nil, nil
	}
	f := os.Getenv(vcslab.EnvCAFile)
	if f == "" {
		return nil, nil
	}
	ca, err := os.ReadFile(f)
	if err != nil {
		return nil, fmt.Errorf("lab CA: %w", err)
	}
	return ca, nil
}
