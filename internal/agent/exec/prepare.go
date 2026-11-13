package exec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
)

// Errors of a run the runner cannot prepare.
var (
	errProjectRoot = errors.New("the project root is not under the workspace")
	errFile        = errors.New("a mounted file cannot be written")
)

// The modes of a mounted file that names none: a write-only file is
// readable by the runner's user alone.
const (
	fileMode          = 0o644
	writeOnlyFileMode = 0o600
)

// DisableStateLock is the variable whose value true plans without the
// state lock.
//
// ref: decision 0010 § "How a run reaches it"
const DisableStateLock = "INFRAWEAVE_DISABLE_STATE_LOCK"

// prepare readies the run's first phase: the vendor's command set, the
// mounted files, the backend file, the phase environment, whose variables
// launcher.mask_envs names the log masks, the plan's JSON file and the
// session. What fails is kept as the first phase's
// failure, so the run fails with it as its note.
func (r *run) prepare(ctx context.Context) {
	r.ready = true
	r.failure = r.setUp(ctx)
}

// setUp is prepare's work; it answers the note of what failed, empty when
// nothing did.
func (r *run) setUp(ctx context.Context) string {
	tool := r.h.Spec.Tool
	if tool == nil {
		return "The run's specification names no tool to run."
	}
	v, err := vendors.Lookup(tool.Name)
	if err != nil {
		return "The runner has no commands for " + string(tool.Name) + "."
	}
	r.vendor = v
	if r.dir, err = r.project(); err != nil {
		return fmt.Sprintf("The project root cannot be used: %v.", err)
	}
	if info, err := os.Stat(r.dir); err != nil || !info.IsDir() {
		return "The project root " + *r.h.Spec.Stack.ProjectRoot + " is not a directory of " +
			"the workspace."
	}
	if err := r.files(); err != nil {
		return fmt.Sprintf("The mounted files cannot be written: %v.", err)
	}
	creds, err := backend.Configure(r.dir, r.h)
	if err != nil {
		return fmt.Sprintf("The state backend cannot be configured: %v.", err)
	}
	r.env = r.environment(creds)
	masked := make([][]byte, 0, len(r.h.MaskEnvs))
	for _, name := range r.h.MaskEnvs {
		masked = append(masked, []byte(lookup(r.env, name)))
	}
	r.log.Add(masked...)
	if r.temp, err = os.MkdirTemp("", "infraweave-run-"); err != nil {
		return fmt.Sprintf("The runner cannot make its temporary directory: %v.", err)
	}
	r.options = vendors.Options{
		LockTimeout: r.h.StateLockTimeout,
		NoLock:      lookup(r.env, DisableStateLock) == "true",
		PlanJSON:    filepath.Join(r.temp, "plan.json"),
	}
	if r.shell, err = start(ctx, r.dir, r.env, r.log.File()); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return "The run's image has no " + Shell + " to run its phases in."
		}
		return fmt.Sprintf("The runner cannot start its shell: %v.", err)
	}
	return ""
}

// environment is the phase environment: the container's, the tool's
// automation switches, the specification's variables with the
// write-only values opened, then the in-run API token and the state
// backend's credentials, which no variable of the stack overrides.
func (r *run) environment(creds []string) []string {
	env := slices.Clone(r.d.Environ)
	env = append(env, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	for _, v := range r.h.Spec.Environment {
		switch {
		case v.WriteOnly:
			env = append(env, v.Name+"="+r.h.Secrets.Environment[v.Name])
		case v.Value != nil:
			env = append(env, v.Name+"="+*v.Value)
		}
	}
	if t := r.h.Secrets.ApiToken; t != "" {
		env = append(env, "INFRAWEAVE_API_TOKEN="+t)
	}
	return append(env, creds...)
}

// lookup is name's value in env, the last entry winning as it does for a
// process.
func lookup(env []string, name string) string {
	for _, e := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v
		}
	}
	return ""
}

// files writes the specification's mounted files under the workspace at
// their modes, a write-only file's content from the opened secrets. The
// workspace is opened as a root, so neither a path nor a link the
// repository commits writes outside it, and a file there is replaced
// rather than followed.
func (r *run) files() error {
	if len(r.h.Spec.Files) == 0 {
		return nil
	}
	root, err := os.OpenRoot(r.d.Workspace)
	if err != nil {
		return fmt.Errorf("open the workspace: %w", err)
	}
	defer root.Close()
	for _, f := range r.h.Spec.Files {
		name := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%w: %s is not under the workspace", errFile, f.Path)
		}
		content, mode := []byte(nil), fs.FileMode(fileMode)
		switch {
		case f.WriteOnly:
			c, ok := r.h.Secrets.Files[f.Path]
			if !ok {
				return fmt.Errorf("%w: %s's content is not in the sealed part", errFile, f.Path)
			}
			content, mode = c, writeOnlyFileMode
		case f.Content != nil:
			content = *f.Content
		}
		if f.FileMode != nil {
			m, err := strconv.ParseUint(*f.FileMode, 8, 32)
			if err != nil || m > 0o777 {
				return fmt.Errorf("%w: %s's mode %q is not octal permission bits", errFile, f.Path,
					*f.FileMode)
			}
			mode = fs.FileMode(m)
		}
		if err := write(root, name, content, mode); err != nil {
			return fmt.Errorf("%w: %s: %w", errFile, f.Path, err)
		}
	}
	return nil
}

// write writes content at name under root with mode, whatever the umask.
func write(root *os.Root, name string, content []byte, mode fs.FileMode) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err //nolint:wrapcheck // the caller names the file
		}
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err //nolint:wrapcheck // as above
	}
	if err := root.WriteFile(name, content, mode); err != nil {
		return err //nolint:wrapcheck // as above
	}
	return root.Chmod(name, mode) //nolint:wrapcheck // as above
}
