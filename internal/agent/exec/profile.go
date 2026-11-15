package exec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/pprof/profile"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// CPUProfileLabel is the sample label a state's CPU profile names the
// command of each sample by: the workflow command's key, or taskCommand
// for a task's command.
//
// ref: decision 0008 § "Logs and artifacts"
const CPUProfileLabel = "infraweave.command"

// taskCommand is the command a task's samples are labelled with.
const taskCommand = "task"

// profiled is the CPU profile file one tool command of a phase was given,
// and the command its samples are labelled with.
type profiled struct {
	command, path string
}

// profiling reports whether the run profiles its tool commands' CPU,
// which the server decides from the stack's settings and the release.
func (r *run) profiling() bool {
	t := r.h.Spec.Tool
	return t != nil && t.EnableCPUProfiling != nil && *t.EnableCPUProfiling
}

// exec runs s in the session. A tool command of a run that profiles runs
// with TOFU_CPU_PROFILE naming a fresh file of the run's temporary
// directory, unset again once it ends, so neither a later command nor a
// hook writes over it.
func (r *run) exec(ctx context.Context, s step) (int, error) {
	if s.command == "" || !r.profiling() {
		return r.shell.run(ctx, s.text)
	}
	r.profiles++
	path := filepath.Join(r.temp, "cpu-"+strconv.Itoa(r.profiles)+".pprof")
	if code, err := r.shell.run(
		ctx,
		"export TOFU_CPU_PROFILE="+quote(path),
	); code != 0 ||
		err != nil {
		return code, err
	}
	r.profiled = append(r.profiled, profiled{command: s.command, path: path})
	code, err := r.shell.run(ctx, s.text)
	if err != nil {
		return code, err
	}
	if c, err := r.shell.run(ctx, "unset TOFU_CPU_PROFILE"); c != 0 || err != nil {
		return c, err
	}
	return code, nil
}

// saveProfile merges the CPU profiles the phase's tool commands wrote,
// each sample labelled with its command, and uploads the result as the
// observability object of state at version, before the phase's report.
// It is best effort: a profile that does not parse, merge or upload is
// noted in the state's log and fails nothing, and a phase whose commands
// wrote none uploads nothing.
//
// ref: decision 0008 § "Logs and artifacts"
func (r *run) saveProfile(ctx context.Context, state wire.WorkerState, version int64) {
	files := r.profiled
	r.profiled = nil
	ps, err := labelled(files)
	if err == nil && len(ps) == 0 {
		return
	}
	var merged *profile.Profile
	if err == nil {
		merged, err = profile.Merge(ps)
	}
	var u upload
	if err == nil {
		u, err = r.sealed(wire.UploadKindObservability,
			kernel.CPUProfileName(kernel.RunState(state)), merged.Write)
	}
	if err == nil {
		err = r.send(ctx, version, []upload{u})
	}
	if err != nil {
		_, _ = fmt.Fprintf(r.log, "infraweave: the %s CPU profile is not saved: %v\n", state, err)
		r.d.Log.WarnContext(ctx, "cpu profile not saved", "run", r.h.Run, "state", state,
			"error", err)
	}
}

// labelled reads each file a command wrote, its samples labelled with the
// command. A command that wrote none, or an empty one, as a tool that
// ignores TOFU_CPU_PROFILE or ends before it writes does, is left out.
func labelled(files []profiled) ([]*profile.Profile, error) {
	out := make([]*profile.Profile, 0, len(files))
	for _, f := range files {
		p, err := read(f.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("the %s command's profile: %w", f.command, err)
		}
		for _, s := range p.Sample {
			if s.Label == nil {
				s.Label = map[string][]string{}
			}
			s.Label[CPUProfileLabel] = []string{f.command}
		}
		out = append(out, p)
	}
	return out, nil
}

// read parses the profile at path; fs.ErrNotExist when the file is
// missing or empty.
func read(path string) (*profile.Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the path
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.Size() == 0 {
		return nil, errors.Join(fs.ErrNotExist, err)
	}
	return profile.Parse(f) //nolint:wrapcheck // the caller names the command
}
