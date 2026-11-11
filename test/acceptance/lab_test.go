package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// labSteps reach the lab a @vcs-lab scenario's @lab: tag names: the
// real provider and repository the vcs-lab job provisioned for the run
// and names through vcslab's variables. They run only in that job.
type labSteps struct {
	// ids are the scenario's kept values, which the requests name as
	// {key}, shared with the organization steps.
	ids map[string]string
}

func (l *labSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the vcs-lab lab's repository$`, l.repository)
}

// repository keeps the lab as {labHost}, {labUser}, {labToken},
// {labNamespace} and {labRepository}, and the commits seed.sh pushed to
// it as {labBase} and {labHead}. A lab whose variables are unset is not
// configured, which fails the scenario.
func (l *labSteps) repository() error {
	lab, err := vcslab.FromEnv(vcslab.EnvNamespace, vcslab.EnvRepository)
	if err != nil {
		return fmt.Errorf("%w: %w", errStep, err)
	}
	for k, v := range map[string]string{
		"labHost": lab.Host, "labUser": lab.User, "labToken": lab.Token,
		"labNamespace": lab.Namespace, "labRepository": lab.Repository,
		"labBase": vcslab.Base, "labHead": vcslab.Head,
	} {
		l.ids[k] = v
	}
	return nil
}

// labVCS adds to vcs the settings a @vcs-lab scenario's servers need to
// reach its lab, and returns the certificates they must trust beyond the
// system's: an @lab:github scenario's servers act as the lab's App,
// whose private key it writes to dir, and every lab's servers trust the
// lab's CA file, when it names one.
func labVCS(dir string, tags []string, vcs map[string]any) ([]byte, error) {
	if !slices.Contains(tags, "@vcs-lab") {
		return nil, nil
	}
	if slices.Contains(tags, "@lab:github") {
		lab, err := vcslab.FromEnv(vcslab.EnvUser, vcslab.EnvToken)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errStep, err)
		}
		key := filepath.Join(dir, "lab-app.pem")
		if err := os.WriteFile(key, []byte(lab.Token), 0o600); err != nil {
			return nil, fmt.Errorf("lab App key: %w", err)
		}
		vcs["github_app"] = map[string]any{"app_id": lab.User, "private_key_file": key}
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
