package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/backend"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
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

// prepare readies the run's first phase: the mounted files, the run's
// workflow, the backend file, the phase environment, whose variables
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
	w, err := vendors.Defaults(tool.Name)
	if err != nil {
		return "The runner has no commands for " + string(tool.Name) + "."
	}
	r.workflow = w
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
	if d := r.readWorkflow(); d != "" {
		return d
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
		Params: workflow.Params{
			PlanFileName:  vendors.PlanFile,
			Lock:          lookup(r.env, DisableStateLock) != "true",
			Refresh:       true,
			WorkspaceName: r.workspaceName(),
			LockTimeout:   r.h.StateLockTimeout,
		},
		PlanJSON: filepath.Join(r.temp, "plan.json"),
	}
	if r.shell, err = start(ctx, r.dir, r.env, r.log.File()); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return "The run's image has no " + Shell + " to run its phases in."
		}
		return fmt.Sprintf("The runner cannot start its shell: %v.", err)
	}
	return ""
}

// readWorkflow replaces the built-in workflow with the project root's
// workflow file when it has one, every key checked before any command
// runs, and answers the note of what fails; empty when nothing does. A
// stack whose workflow tool is CUSTOM must have the file. The project
// root is opened as a root, so a link the repository commits reads
// nothing outside it, and no more of the file than workflow.MaxSize and
// one byte is read.
//
// ref: decision 0024 "Workflow command templates"
func (r *run) readWorkflow() string {
	data, err := readFile(r.dir, workflow.File, workflow.MaxSize+1)
	switch {
	case errors.Is(err, fs.ErrNotExist) && r.setting(workflowTool) == customTool:
		return "The workflow file " + workflow.File + " is missing, and the stack's workflow " +
			"tool is " + customTool + "."
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err != nil:
		return fmt.Sprintf("The workflow file %s cannot be read: %v.", workflow.File, err)
	}
	w, err := vendors.Read(data)
	if err != nil {
		return fmt.Sprintf("The workflow file %s cannot be used: %s.", workflow.File,
			strings.TrimSuffix(err.Error(), "."))
	}
	r.workflow = w
	return ""
}

// The stack's vendor settings the runner reads: the workflow tool, whose
// value CUSTOM runs only a workflow file's commands, and the workspace,
// which templates read as workspaceName.
const (
	workflowTool     = "workflowTool"
	customTool       = "CUSTOM"
	workspaceSetting = "workspace"
)

// setting is the stack's vendor setting name, empty when it is not a
// string.
func (r *run) setting(name string) string {
	s, _ := r.h.Spec.Stack.VendorConfig[name].(string)
	return s
}

// workspaceName is the stack's workspace, or workflow.DefaultWorkspace.
func (r *run) workspaceName() string {
	if ws := r.setting(workspaceSetting); ws != "" {
		return ws
	}
	return workflow.DefaultWorkspace
}

// readFile reads at most limit bytes of name under dir, opened as a root.
func readFile(dir, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open the project root: %w", err)
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the file
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return data, nil
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
