package exec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// The modes of a scoped file and of its directory once the runner has
// written it: the run's user reads them and writes neither.
const (
	scopedFileMode = 0o400
	scopedDirMode  = 0o500
	openDirMode    = 0o700
)

// errScoped marks a scoped file the runner cannot write or remove, and
// errStillPresent one still at its path once removed.
var (
	errScoped       = errors.New("a scoped file cannot be exposed")
	errStillPresent = errors.New("it is still present once removed")
)

// passthrough are the handoff's passthrough variables of exposure e that
// no specification variable shares a name with: a stack's or a
// context's variable takes precedence over the launcher's.
func (r *run) passthrough(e phaseinputs.Exposure) []phaseinputs.Variable {
	var out []phaseinputs.Variable
	for _, v := range r.h.Inputs.Variables {
		if v.Exposure != e || slices.ContainsFunc(r.h.Spec.Environment, func(s wire.RunSpecVariable) bool {
			return s.Name == v.Name
		}) {
			continue
		}
		out = append(out, v)
	}
	return out
}

// task reports whether the run is a task's, whose every phase admits
// write exposure.
func (r *run) task() bool {
	return r.h.Spec.Task != nil || r.h.Spec.Run.Type == wire.TASK
}

// expose readies state's phase for the inputs its exposure admits, before
// any of its steps: when the exposure differs from the phase before's, it
// unsets the other side's passthrough variables in the session, removes
// its scoped files and checks they are gone, then writes this side's
// files and exports its variables. It answers the phase's failure note,
// empty when nothing fails.
//
// ref: decision 0008 § "Phase-scoped inputs"
func (r *run) expose(ctx context.Context, state wire.WorkerState) string {
	p := phaseinputs.Phase(state, r.task())
	if p == r.exposure {
		return ""
	}
	var unset []string
	if r.exposure != "" {
		for _, v := range r.passthrough(r.exposure) {
			unset = append(unset, v.Name)
		}
	}
	if err := r.scopedFiles(p); err != nil {
		return fmt.Sprintf("The phase's scoped files cannot be exposed: %v.", err)
	}
	r.exposure = p
	if len(unset) > 0 {
		if d := r.steps(ctx, []step{{
			text: "unset " + strings.Join(unset, " "), what: "Removing the other phases' passthrough variables",
		}}); d != "" {
			return d
		}
	}
	return r.exportScoped(ctx)
}

// exportScoped exports the passthrough variables of the current
// exposure in the session, and answers the failure note of the step,
// empty when it succeeds or there are none.
func (r *run) exportScoped(ctx context.Context) string {
	vars := r.passthrough(r.exposure)
	if r.exposure == "" || len(vars) == 0 {
		return ""
	}
	exports := make([]string, len(vars))
	for i, v := range vars {
		exports[i] = v.Name + "=" + quote(v.Value)
	}
	return r.steps(ctx, []step{{
		text: "export " + strings.Join(exports, " "), what: "Setting the phase's passthrough variables",
	}})
}

// scopedFiles removes every scoped file exposure p does not admit and
// checks that it is gone, then writes every one it does, each in its
// directory, which the executor provides to the runner alone. A file is
// created afresh, never through a link that is in its place.
func (r *run) scopedFiles(p phaseinputs.Exposure) error {
	files := slices.Clone(r.h.Inputs.Files)
	// Removals come first, so no phase ever sees both sides.
	slices.SortStableFunc(files, func(a, b phaseinputs.File) int {
		switch {
		case a.Exposure.Admits(p) == b.Exposure.Admits(p):
			return 0
		case a.Exposure.Admits(p):
			return 1
		}
		return -1
	})
	for _, f := range files {
		dir, name := path.Split(f.Target)
		root, err := os.OpenRoot(dir)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", errScoped, f.Target, err)
		}
		if f.Exposure.Admits(p) {
			err = writeScoped(root, dir, name, f.Content)
		} else {
			err = removeScoped(root, dir, name)
		}
		_ = root.Close()
		if err != nil {
			return fmt.Errorf("%w: %s: %w", errScoped, f.Target, err)
		}
	}
	return nil
}

// writeScoped writes content as name under root, the directory dir, read
// by the run's user alone, and leaves dir unwritable.
func writeScoped(root *os.Root, dir, name string, content []byte) error {
	// Each executor gives the run's user the directory, so a mode it
	// cannot set is a directory a hook could write in.
	if err := os.Chmod(dir, openDirMode); err != nil { //nolint:gosec // the run's user alone reads a scoped directory
		return err //nolint:wrapcheck // scopedFiles names the file
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err //nolint:wrapcheck // scopedFiles names the file
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, scopedFileMode)
	if err != nil {
		return err //nolint:wrapcheck // as above
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err //nolint:wrapcheck // as above
	}
	return os.Chmod(dir, scopedDirMode) //nolint:wrapcheck // as above
}

// removeScoped removes name under root, the directory dir, and checks
// that nothing is left at its path.
func removeScoped(root *os.Root, dir, name string) error {
	if err := os.Chmod(dir, openDirMode); err != nil { //nolint:gosec // as writeScoped
		return err //nolint:wrapcheck // scopedFiles names the file
	}
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err //nolint:wrapcheck // scopedFiles names the file
	}
	switch _, err := root.Lstat(name); {
	case err == nil:
		return errStillPresent
	case !errors.Is(err, fs.ErrNotExist):
		return err //nolint:wrapcheck // as above
	}
	return os.Chmod(dir, scopedDirMode) //nolint:wrapcheck // as above
}
