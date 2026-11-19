package vcslab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/weaveplatform/infraweave/internal/platform/private"
)

// TerraformHooks applies only the lab root's optional hook configuration after
// the served API creates an integration. Its private variable file and captured
// tool output never reach a public step error or a command-line argument value.
type TerraformHooks struct{ Root, Directory string }

// Set supplies the current integration destination; an empty destination removes
// all hooks. The root's existing suffix/credentials remain in its environment.
func (t TerraformHooks) Set(ctx context.Context, destination, secret string) error {
	if err := os.MkdirAll(t.Directory, 0o700); err != nil {
		return fmt.Errorf("%w: create private Terraform directory", ErrHook)
	}
	variables, err := json.Marshal(map[string]string{"hook_url": destination, "hook_secret": secret})
	if err != nil {
		return fmt.Errorf("%w: encode hook variables", ErrHook)
	}
	defer clear(variables)
	file, err := os.CreateTemp(t.Directory, "hooks-*.tfvars.json")
	if err != nil {
		return fmt.Errorf("%w: create private hook variables", ErrHook)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err := private.Restrict(name); err != nil {
		_ = file.Close()
		return fmt.Errorf("%w: restrict hook variables", ErrHook)
	}
	_, writeErr := file.Write(variables)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("%w: write hook variables", ErrHook)
	}
	log, err := os.OpenFile(filepath.Join(t.Directory, "terraform-hooks.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create private hook diagnostics", ErrHook)
	}
	defer func() { _ = log.Close() }()
	if err := private.Restrict(log.Name()); err != nil {
		return fmt.Errorf("%w: restrict hook diagnostics", ErrHook)
	}
	cmd := exec.CommandContext(ctx, "terraform", "-chdir="+t.Root, "apply", "-input=false", "-auto-approve", "-no-color", "-var-file="+name)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: apply repository hooks; private diagnostics retained", ErrHook)
	}
	return nil
}
