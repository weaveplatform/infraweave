package launcher_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/weaveplatform/infraweave/internal/agent/config"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// fakeInputs answers res, or err.
type fakeInputs struct {
	res launcher.Resolved
	err error
}

func (f *fakeInputs) Resolve(context.Context) (launcher.Resolved, error) {
	// Each assignment gets its own copy: the launcher clears the files.
	res := f.res
	res.Inputs.Files = slices.Clone(res.Inputs.Files)
	for i := range res.Inputs.Files {
		res.Inputs.Files[i].Content = bytes.Clone(res.Inputs.Files[i].Content)
	}
	return res, f.err
}

func TestUnitLauncherHandsOverPhaseInputs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		in := &fakeInputs{res: launcher.Resolved{
			Inputs: phaseinputs.Inputs{
				Variables: []phaseinputs.Variable{
					{Name: "TOKEN", Exposure: phaseinputs.Read, Value: "r"},
					{Name: "TOKEN", Exposure: phaseinputs.Write, Value: "w", Sensitive: true},
				},
				Files: []phaseinputs.File{
					{Target: "/mnt/keys/read", Exposure: phaseinputs.Read, Content: []byte("r")},
					{Target: "/mnt/keys/write", Exposure: phaseinputs.Write, Content: []byte("w")},
				},
			},
			Mounts:  []executor.Mount{{Source: "/host/cache", Target: "/mnt/cache", ReadOnly: true}},
			Volumes: []executor.Volume{{Kind: executor.PVC, Name: "data", Target: "/mnt/data"}},
		}}
		w.inputs = in
		r := w.start(t)
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		run.Spec.Stack.Vendor = "opentofu"
		run.Spec.Tool = &wire.RunSpecTool{Name: wire.Tofu, Version: "1.13.9", Download: true}
		w.queue(t, run)
		j := w.exec.Jobs()[0]
		if want := []executor.Mount{
			{Source: "/cache/tofu/1.13.9/linux_arm64", Target: "/bin/tofu", ReadOnly: true},
			{Source: "/host/cache", Target: "/mnt/cache", ReadOnly: true},
		}; !slices.Equal(j.Mounts, want) {
			t.Errorf("mounts: %+v", j.Mounts)
		}
		if !slices.Equal(j.Volumes, in.res.Volumes) || !slices.Equal(j.Scoped, []string{"/mnt/keys"}) ||
			j.Execution != (executor.Execution{Vendor: "opentofu", Tool: "tofu"}) {
			t.Errorf("job: volumes %+v, scoped %q, execution %+v", j.Volumes, j.Scoped, j.Execution)
		}
		h, err := handoff.Read(bytes.NewReader(j.Handoff))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(h.Inputs, in.res.Inputs) {
			t.Errorf("handoff's inputs: %+v", h.Inputs)
		}
		if !strings.Contains(w.log.String(), `msg="run container started" run=run-1 runtime=unknown `+
			`vendor=opentofu tool=tofu tool_version=1.13.9 tool_source=release`) {
			t.Errorf("log: %s", w.log)
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		w.answer(wire.PollResult{})

		// Inputs that cannot be resolved, or that conflict, fail the
		// preparation before any container.
		for i, c := range []struct {
			res  launcher.Resolved
			err  error
			want string
		}{
			{err: errors.New("the Secret keys: not found"), want: "the pool's phase-scoped inputs: the Secret keys: not found"},
			{res: launcher.Resolved{
				Mounts: []executor.Mount{{Source: "/host", Target: "/mnt/keys", ReadOnly: true}},
				Inputs: phaseinputs.Inputs{Files: []phaseinputs.File{{Target: "/mnt/keys/k", Exposure: phaseinputs.Read}}},
			}, want: "overlap"},
			{res: launcher.Resolved{
				Inputs: phaseinputs.Inputs{Variables: []phaseinputs.Variable{{Name: "INFRAWEAVE_X", Exposure: phaseinputs.All}}},
			}, want: "names a variable the runner sets"},
		} {
			in.res, in.err = c.res, c.err
			w.assign(t, "run-"+string(rune('2'+i)), wire.AssignmentStatePREPARING, "")
			reps := w.srv.Reports()
			rep := reps[len(reps)-1]
			if rep.Report.Event != wire.Failed || !strings.Contains(*rep.Report.Details, c.want) {
				t.Errorf("case %d: %+v %s", i, rep.Report, *rep.Report.Details)
			}
			w.wantPoll(t, false, "", nil)
		}
		if len(w.exec.Jobs()) != 1 {
			t.Errorf("jobs: %d", len(w.exec.Jobs()))
		}
		r.stop(t)
	})
}

func TestUnitLauncherNamesTheExecution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		run.Spec.Stack.Vendor = "pulumi"
		w.queue(t, run)
		if got := w.exec.Jobs()[0].Execution; got != (executor.Execution{Vendor: "unknown", Tool: "unknown"}) {
			t.Errorf("execution: %+v", got)
		}
		if !strings.Contains(w.log.String(), `vendor=unknown tool=unknown tool_version="" tool_source=none`) {
			t.Errorf("log: %s", w.log)
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		w.answer(wire.PollResult{})

		run = newRun("run-2", wire.AssignmentStatePREPARING, "")
		run.Spec.Stack.Vendor = "terragrunt"
		run.Spec.Tool = &wire.RunSpecTool{Name: wire.Terraform, Version: "1.17.2"}
		w.queue(t, run)
		if got := w.exec.Jobs()[1].Execution; got != (executor.Execution{Vendor: "terragrunt", Tool: "terraform"}) {
			t.Errorf("execution: %+v", got)
		}
		if !strings.Contains(w.log.String(), `vendor=terragrunt tool=terraform tool_version="" tool_source=image`) {
			t.Errorf("log: %s", w.log)
		}
		r.stop(t)
	})
}

func TestUnitHostInputs(t *testing.T) {
	dir := t.TempDir()
	file := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read, write := file("read", []byte("r")), file("write", []byte("w"))
	env := map[string]string{"ro_TOKEN": "r", "wo_TOKEN": "w", "CACHE": "/c"}
	c := config.Defaults().Launcher
	c.WhitelistEnvs = []string{"ro_TOKEN", "wo_TOKEN", "CACHE", "UNSET"}
	c.MaskEnvs = []string{"wo_TOKEN", "CACHE"}
	c.Docker.ExtraMounts = []string{"/host/cache:/mnt/cache:ro"}
	c.Docker.ROExtraMounts = []string{read + ":/mnt/keys/read"}
	c.Docker.WOExtraMounts = []string{write + ":/mnt/keys/write"}
	h := launcher.Host{Config: c, Environ: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}}
	res, err := h.Resolve(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := launcher.Resolved{
		Inputs: phaseinputs.Inputs{
			Variables: []phaseinputs.Variable{
				{Name: "TOKEN", Exposure: phaseinputs.Read, Value: "r"},
				{Name: "TOKEN", Exposure: phaseinputs.Write, Value: "w", Sensitive: true},
				{Name: "CACHE", Exposure: phaseinputs.All, Value: "/c", Sensitive: true},
			},
			Files: []phaseinputs.File{
				{Target: "/mnt/keys/read", Exposure: phaseinputs.Read, Content: []byte("r"), Sensitive: true},
				{Target: "/mnt/keys/write", Exposure: phaseinputs.Write, Content: []byte("w"), Sensitive: true},
			},
		},
		Mounts: []executor.Mount{{Source: "/host/cache", Target: "/mnt/cache", ReadOnly: true}},
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("resolved:\n got %+v\nwant %+v", res, want)
	}
	if err := res.Check(); err != nil {
		t.Errorf("check: %v", err)
	}

	// A scoped mount must be a regular file within the bound.
	for _, source := range []string{
		dir, filepath.Join(dir, "missing"), file("big", bytes.Repeat([]byte{'x'}, phaseinputs.MaxFile+1)),
	} {
		c.Docker.ROExtraMounts = []string{source + ":/mnt/keys/read"}
		h.Config = c
		if _, err := h.Resolve(t.Context()); err == nil || !strings.Contains(err.Error(), source) {
			t.Errorf("%s: %v", source, err)
		}
	}

	// The launcher's own environment is the default.
	t.Setenv("ro_TOKEN", "from-process")
	res, err = launcher.Host{Config: config.Launcher{WhitelistEnvs: []string{"ro_TOKEN"}}}.Resolve(t.Context())
	if err != nil || len(res.Inputs.Variables) != 1 || res.Inputs.Variables[0].Value != "from-process" {
		t.Errorf("process environment: %+v, %v", res, err)
	}
}
