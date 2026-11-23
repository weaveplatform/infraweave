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
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/vendors"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/iac/workflow"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
)

// Errors of a run the runner cannot prepare.
var (
	errProjectRoot = errors.New("the project root is not under the checkout")
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
	if err == nil && r.terragrunt() {
		w, err = vendors.Terragrunt(r.h.Spec.Terragrunt.RunAll)
	}
	if err != nil {
		return "The runner has no commands for " + string(tool.Name) + "."
	}
	r.workflow = w
	if r.dir, err = r.project(); err != nil {
		return fmt.Sprintf("The project root cannot be used: %v.", err)
	}
	if info, err := os.Stat(r.dir); err != nil || !info.IsDir() {
		name := "."
		if p := r.h.Spec.Stack.ProjectRoot; p != nil && *p != "" {
			name = *p
		}
		return "The project root " + name + " is not a directory of the checkout."
	}

	if err := r.files(); err != nil {
		return fmt.Sprintf("The mounted files cannot be written: %v.", err)
	}
	if r.terragrunt() && !r.h.Spec.Terragrunt.RunAll {
		info, err := os.Stat(filepath.Join(r.dir, backend.UnitFile))
		if err != nil || !info.Mode().IsRegular() {
			name := "."
			if p := r.h.Spec.Stack.ProjectRoot; p != nil && *p != "" {
				name = *p
			}
			return "The project root " + name + " holds no terragrunt.hcl; a Terragrunt " +
				"stack whose useRunAll is false runs the unit there."
		}
	}
	if d := r.readWorkflow(); d != "" {
		return d
	}
	creds, err := backend.Configure(r.d.Workspace, r.dir, r.h)
	if err != nil {
		return fmt.Sprintf("The state backend cannot be configured: %v.", err)
	}
	if r.temp, err = os.MkdirTemp("", "infraweave-run-"); err != nil {
		return fmt.Sprintf("The runner cannot make its temporary directory: %v.", err)
	}
	if r.env, err = r.environment(creds); err != nil {
		return fmt.Sprintf("The run environment cannot be configured: %v.", err)
	}
	masked := make([][]byte, 0, len(r.h.MaskEnvs))
	for _, name := range r.h.MaskEnvs {
		masked = append(masked, []byte(lookup(r.env, name)))
	}
	r.log.Add(masked...)
	var targets []string
	if t := r.h.Spec.Targets; t != nil {
		targets = *t
	}
	r.options = vendors.Options{
		Params: workflow.Params{
			PlanFileName:  vendors.PlanFile,
			Lock:          lookup(r.env, DisableStateLock) != "true",
			Refresh:       true,
			WorkspaceName: r.workspaceName(),
			LockTimeout:   r.h.StateLockTimeout,
			Targets:       targets,
			Destroy:       r.h.Spec.Run.Type == wire.DESTROY,
		},
		PlanJSON:    filepath.Join(r.temp, "plan.json"),
		OutputsJSON: filepath.Join(r.temp, "outputs.json"),
	}
	if r.terragrunt() {
		r.options.Params.PlanFileName = workflow.TerragruntPlanFile
		r.options.UnitPlans = filepath.Join(r.temp, "plans")
	}
	if r.shell, err = start(ctx, r.dir, r.env, r.log.File()); err != nil {
		if errors.Is(err, osexec.ErrNotFound) {
			return "The run's image has no " + Shell + " to run its phases in."
		}
		return fmt.Sprintf("The runner cannot start its shell: %v.", err)
	}
	r.attach(r.shell)
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
// automation switches, for a Terragrunt run the tool Terragrunt drives
// and Terragrunt's own switch, the runner's user agent and API endpoint,
// the launcher's run settings, SSL_CERT_FILE trusting the handoff's roots
// when it has any, the passthrough variables every phase sees, the
// specification's variables with the
// write-only values opened, then the in-run API token and the state
// backend's credentials, which no variable of the stack overrides.
//
// ref: decision 0008 § "Phases, reports and the plan decision"
func (r *run) environment(creds []string) ([]string, error) {
	api, err := backend.APIAddress(r.h.Endpoint)
	if err != nil {
		return nil, err //nolint:wrapcheck // setUp names the environment and Address names the URL
	}
	env := slices.Clone(r.d.Environ)
	env = append(env, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
	if r.terragrunt() {
		env = append(env, "TG_TF_PATH="+r.toolPath(), "TG_NON_INTERACTIVE=true")
	}
	env = append(env,
		"TF_APPEND_USER_AGENT=infraweave/"+buildinfo.Version(),
		kernel.APIEndpointVariable+"="+api,
		"INFRAWEAVE_MAX_REQUESTS_PER_SECOND="+strconv.Itoa(r.h.MaxRequestsPerSecond),
		"INFRAWEAVE_MAX_REQUESTS_BURST="+strconv.Itoa(r.h.MaxRequestsBurst),
		"INFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED="+strconv.FormatBool(r.h.SensitiveOutputUpload),
		"INFRAWEAVE_VENDOR_BINARY_CACHED="+strconv.FormatBool(r.h.Spec.Tool.Download),
	)
	if len(r.h.Roots) > 0 {
		bundle, err := r.roots(env)
		if err != nil {
			return nil, err
		}
		env = append(env, "SSL_CERT_FILE="+bundle)
	}
	for _, v := range r.h.Inputs.Variables {
		if v.Exposure == phaseinputs.All {
			env = append(env, v.Name+"="+v.Value)
		}
	}
	for _, v := range r.h.Spec.Environment {
		switch {
		case v.WriteOnly:
			env = append(env, v.Name+"="+r.h.Secrets.Environment[v.Name])
		case v.Value != nil:
			env = append(env, v.Name+"="+*v.Value)
		}
	}
	if t := r.h.Secrets.ApiToken; t != "" {
		env = append(env, kernel.APITokenVariable+"="+t)
	}
	return append(env, creds...), nil
}

// terragrunt reports whether the run is a Terragrunt stack's, whose
// commands Terragrunt runs according to its resolved RunAll mode.
func (r *run) terragrunt() bool {
	return r.h.Spec.Terragrunt != nil
}

// toolPath is the tool Terragrunt drives: the binary the launcher mounts
// at /bin/<tool> when the specification downloads it, else the image's
// own on the path.
//
// ref: decision 0008 § "Runner images"
func (r *run) toolPath() string {
	t := r.h.Spec.Tool
	if t.Download {
		return "/bin/" + string(t.Name)
	}
	return string(t.Name)
}

// lookup is name's value in env, the last entry winning as it does for a
// process.
// systemBundles are where Linux distributions keep their trusted roots,
// in the order Go's crypto/x509 reads them.
var systemBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/tls/cacert.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// roots writes the run image's trusted roots, SSL_CERT_FILE's in env or
// the first system bundle it has, with the handoff's, to the runner's
// temporary directory, and answers the file's path: the run's tools
// read it as SSL_CERT_FILE, which replaces their default bundle.
func (r *run) roots(env []string) (string, error) {
	bundles := systemBundles
	if f := lookup(env, "SSL_CERT_FILE"); f != "" {
		bundles = []string{f}
	}
	var system []byte
	for _, f := range bundles {
		if b, err := os.ReadFile(f); err == nil {
			system = b
			break
		}
	}
	path := filepath.Join(r.temp, "roots.pem")
	bundle := append(append(system, '\n'), r.h.Roots...)
	if err := os.WriteFile(path, bundle, 0o600); err != nil { //nolint:gosec // a fixed name in the runner's own temporary directory
		return "", fmt.Errorf("the trusted roots: %w", err)
	}
	return path, nil
}

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
// rather than followed. A path under source/ replaces a checkout file.
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
