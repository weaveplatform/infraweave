package phaseinputs

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Exposure is the execution phases an input reaches: every phase, the
// read phases, or the write phases.
type Exposure string

// The exposures. A configured name's ReadPrefix or WritePrefix declares
// Read or Write; an unprefixed one has All.
const (
	All   Exposure = "all"
	Read  Exposure = "read"
	Write Exposure = "write"
)

// The prefixes of a configured name, which the child's name drops.
const (
	ReadPrefix  = "ro_"
	WritePrefix = "wo_"
)

// The bounds of the scoped files one assignment receives: each file's
// content, which matches a Kubernetes Secret's, and all of them together,
// which the handoff carries in memory.
const (
	MaxFile  = 1 << 20
	MaxFiles = 16 << 20
)

// Variable is a passthrough variable: the name the run sees, its
// exposure, its value, and whether the run's log masks the value.
type Variable struct {
	Name      string   `json:"name"`
	Exposure  Exposure `json:"exposure"`
	Value     string   `json:"value"`
	Sensitive bool     `json:"sensitive,omitempty"`
}

// File is a scoped file: the absolute path the run sees it at, its
// exposure, which is Read or Write, its content, and whether the run's
// log masks the content.
type File struct {
	Target    string   `json:"target"`
	Exposure  Exposure `json:"exposure"`
	Content   []byte   `json:"content"`
	Sensitive bool     `json:"sensitive,omitempty"`
}

// Inputs are an assignment's phase-scoped inputs, which the handoff
// carries to the runner.
type Inputs struct {
	Variables []Variable `json:"variables,omitempty"`
	Files     []File     `json:"files,omitempty"`
}

// ErrInputs marks a configuration of inputs the launcher refuses.
var ErrInputs = errors.New("invalid phase-scoped inputs")

// Name splits a configured name into the name the run sees and the
// exposure its prefix declares.
func Name(configured string) (string, Exposure) {
	if n, ok := strings.CutPrefix(configured, ReadPrefix); ok {
		return n, Read
	}
	if n, ok := strings.CutPrefix(configured, WritePrefix); ok {
		return n, Write
	}
	return configured, All
}

// ParseExposure is s as an exposure.
func ParseExposure(s string) (Exposure, error) {
	switch e := Exposure(s); e {
	case All, Read, Write:
		return e, nil
	}
	return "", fmt.Errorf("%w: exposure %q is not all, read or write", ErrInputs, s)
}

// Phase is the exposure the phase of state admits beside All: Write for
// applying, destroying and performing, and for every phase of a task
// run, whose initialization serves arbitrary, potentially mutating work;
// Read for initialization and planning of every other run.
//
// ref: decision 0008 § "Phase-scoped inputs"
func Phase(state wire.WorkerState, task bool) Exposure {
	if task {
		return Write
	}
	switch state {
	case wire.WorkerStateAPPLYING, wire.WorkerStateDESTROYING, wire.WorkerStatePERFORMING:
		return Write
	}
	return Read
}

// Admits reports whether an input of exposure e reaches a phase that
// admits p.
func (e Exposure) Admits(p Exposure) bool { return e == All || e == p }

// overlaps reports whether some phase admits both a and b.
func overlaps(a, b Exposure) bool { return a == All || b == All || a == b }

// envName is a variable's name, as a shell takes it.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// managed are the variables the runner sets itself, beside every
// INFRAWEAVE_ and TF_VAR_infraweave_ variable.
var managed = []string{
	"TF_IN_AUTOMATION", "TF_INPUT", "TF_APPEND_USER_AGENT", "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD",
	"TG_TF_PATH", "TG_NON_INTERACTIVE", "TG_JSON_OUT_DIR", "TOFU_CPU_PROFILE", "SSL_CERT_FILE",
}

// Managed reports whether the runner sets the variable name itself, so
// no passthrough variable may.
func Managed(name string) bool {
	return strings.HasPrefix(name, "INFRAWEAVE_") || strings.HasPrefix(name, "TF_VAR_infraweave_") ||
		slices.Contains(managed, name)
}

// CheckVariable checks one configured variable: the name it is
// configured as, which a prefix may scope, and the exposure configured
// beside it, empty when the prefix alone declares it, which must agree
// with the prefix. It answers the name the run sees and the exposure.
func CheckVariable(configured, exposure string) (string, Exposure, error) {
	name, e := Name(configured)
	if !envName.MatchString(configured) || !envName.MatchString(name) {
		return "", "", fmt.Errorf("%w: %q is not a variable's name", ErrInputs, configured)
	}
	if exposure != "" && Exposure(exposure) != e {
		return "", "", fmt.Errorf("%w: %q has the exposure %s, but its prefix declares %s",
			ErrInputs, configured, exposure, e)
	}
	if Managed(name) {
		return "", "", fmt.Errorf("%w: %q names a variable the runner sets", ErrInputs, configured)
	}
	return name, e, nil
}

// CheckNames refuses two configured variables that one phase would see
// under the same name: the same child name with exposures that overlap.
// A read and a write variable of one name never meet.
func CheckNames(configured []string) error {
	seen := map[string][]Exposure{}
	for _, c := range configured {
		name, e := Name(c)
		for _, other := range seen[name] {
			if overlaps(e, other) {
				return fmt.Errorf("%w: %q collides with another variable named %s in one phase",
					ErrInputs, c, name)
			}
		}
		seen[name] = append(seen[name], e)
	}
	return nil
}

// Reserved are the paths a mount's target may neither equal, lie under,
// nor hold: the infraweave executable, the tools that replace the
// image's, the workspace, and the kernel's own filesystems.
var Reserved = []string{
	"/opt/infraweave", "/run/infraweave", "/bin", "/usr/bin", "/usr/local/bin", "/sbin",
	"/usr/sbin", "/mnt/workspace", "/proc", "/sys", "/dev",
}

// related reports whether a and b are the same path or one holds the
// other.
func related(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// CheckTarget checks a mount's target: absolute, canonical, not the root
// and apart from every Reserved path.
func CheckTarget(target string) error {
	if !path.IsAbs(target) || path.Clean(target) != target || target == "/" {
		return fmt.Errorf("%w: the target %q is not a canonical absolute path", ErrInputs, target)
	}
	for _, r := range Reserved {
		if related(target, r) {
			return fmt.Errorf("%w: the target %q overlaps %s", ErrInputs, target, r)
		}
	}
	return nil
}

// Dir is the directory that holds a scoped file's target, which the
// executor provides empty, in memory and writable by the run's user
// alone, so it holds nothing but the scoped files the runner writes.
func Dir(target string) string { return path.Dir(target) }

// CheckTargets checks every mount's target together: mounted are the
// targets every phase sees mounted, scoped the scoped files'. Each is a
// valid target and no two are related; a scoped file's directory is
// below the root's own directories, apart from every reserved path, and
// unrelated to every mounted target, so no mount hides a scoped file or
// sits in a directory the runner rewrites.
func CheckTargets(mounted, scoped []string) error {
	all := append(slices.Clone(mounted), scoped...)
	for i, t := range all {
		if err := CheckTarget(t); err != nil {
			return err
		}
		for _, u := range all[:i] {
			if related(t, u) {
				return fmt.Errorf("%w: the targets %q and %q overlap", ErrInputs, u, t)
			}
		}
	}
	for _, t := range scoped {
		dir := Dir(t)
		if path.Dir(dir) == "/" {
			return fmt.Errorf("%w: the scoped file %q needs a directory of its own below %s",
				ErrInputs, t, dir)
		}
		for _, r := range Reserved {
			if related(dir, r) {
				return fmt.Errorf("%w: the scoped file %q's directory overlaps %s", ErrInputs, t, r)
			}
		}
		for _, m := range mounted {
			if related(dir, m) {
				return fmt.Errorf("%w: the scoped file %q's directory overlaps the mount %q",
					ErrInputs, t, m)
			}
		}
	}
	return nil
}

// Dirs are the distinct directories of files' targets, sorted.
func Dirs(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, Dir(f.Target))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Check checks in: each variable's name and exposure, their collisions,
// each file's exposure, target and size, and the files' total size. A
// file's exposure is Read or Write: a file every phase sees is mounted.
func Check(in Inputs) error {
	names := make([]string, 0, len(in.Variables))
	for _, v := range in.Variables {
		configured := v.Name
		switch v.Exposure {
		case Read:
			configured = ReadPrefix + v.Name
		case Write:
			configured = WritePrefix + v.Name
		}
		if _, _, err := CheckVariable(configured, string(v.Exposure)); err != nil {
			return err
		}
		names = append(names, configured)
	}
	if err := CheckNames(names); err != nil {
		return err
	}
	targets := make([]string, 0, len(in.Files))
	total := 0
	for _, f := range in.Files {
		if f.Exposure != Read && f.Exposure != Write {
			return fmt.Errorf("%w: the scoped file %q has the exposure %q", ErrInputs, f.Target, f.Exposure)
		}
		if len(f.Content) > MaxFile {
			return fmt.Errorf("%w: the scoped file %q holds more than %d bytes", ErrInputs, f.Target, MaxFile)
		}
		total += len(f.Content)
		targets = append(targets, f.Target)
	}
	if total > MaxFiles {
		return fmt.Errorf("%w: the scoped files hold more than %d bytes", ErrInputs, MaxFiles)
	}
	return CheckTargets(nil, targets)
}
