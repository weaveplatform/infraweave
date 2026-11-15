//go:build unix

package exec_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/pprof/profile"

	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// profilingTool is fakeTool as tofu, which also copies $CPU_<subcommand>,
// when set, to the file TOFU_CPU_PROFILE names, as OpenTofu writes its
// CPU profile there.
var profilingTool = strings.Replace(fakeTool, `eval "exit`, `eval "src=\${CPU_$1:-}"
[ -n "$TOFU_CPU_PROFILE" ] && [ -n "$src" ] && cp "$src" "$TOFU_CPU_PROFILE"
eval "exit`, 1)

// profiled makes w's run an OpenTofu run, profiled when on, whose tool
// writes the profile of n samples in a function of its subcommand's name
// for each subcommand of samples.
func (w *world) profiled(on bool, samples map[string]int64) {
	w.t.Helper()
	bin := strings.TrimPrefix(w.environ[0], "PATH=")
	bin = bin[:strings.IndexByte(bin, ':')]
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(profilingTool), 0o755); err != nil {
		w.t.Fatal(err)
	}
	w.h.Spec.Tool = &wire.RunSpecTool{Name: wire.Tofu, Version: "1.11.0", EnableCPUProfiling: &on}
	for sub, n := range samples {
		w.environ = append(w.environ, "CPU_"+sub+"="+cpuProfile(w.t, sub, n))
	}
}

// cpuProfile writes a CPU profile of n samples in function fn and answers
// its path.
func cpuProfile(t *testing.T, fn string, n int64) string {
	t.Helper()
	f := &profile.Function{ID: 1, Name: fn}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: f}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{
			{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"},
		},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     10_000_000,
		Function:   []*profile.Function{f},
		Location:   []*profile.Location{loc},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{loc}, Value: []int64{n, n * 10_000_000}},
		},
	}
	path := filepath.Join(t.TempDir(), fn+".pprof")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := p.Write(file); err != nil {
		t.Fatal(err)
	}
	return path
}

// samples are the samples of the profile w's run uploaded as name, by
// the command their label names; nil when it uploaded none.
func (w *world) samples(name string) map[string]int64 {
	w.t.Helper()
	b, ok := w.w.recorded[name]
	if !ok {
		return nil
	}
	p, err := profile.ParseData(b)
	if err != nil {
		w.t.Fatalf("%s: %v", name, err)
	}
	out := map[string]int64{}
	for _, s := range p.Sample {
		if l := s.Label[exec.CPUProfileLabel]; len(l) == 1 {
			out[l[0]] += s.Value[0]
		} else {
			out["unlabelled"] += s.Value[0]
		}
	}
	return out
}

// artifacts are the records the fake worker took, as "artifacts
// <version>: <kind> <name>, ...".
func (w *world) artifacts() []string {
	var out []string
	for _, e := range w.w.taken {
		if strings.HasPrefix(e, "artifacts ") {
			out = append(out, e)
		}
	}
	return out
}

// TestUnitExecCPUProfiles profiles each tool command of a run that
// profiles with a fresh TOFU_CPU_PROFILE, unset again for its hooks, and
// uploads each state's profiles merged, before the state's report, each
// sample labelled with its workflow command, or task for a task's.
func TestUnitExecCPUProfiles(t *testing.T) {
	t.Run("a tracked run", func(t *testing.T) {
		w := newWorld(t)
		w.profiled(true, map[string]int64{"init": 2, "plan": 5, "show": 1})
		w.h.Spec.Hooks = map[string][]string{
			"after_plan": {hook("after_plan", "TOFU_CPU_PROFILE")},
		}
		if err := w.run(); err != nil {
			t.Fatal(err)
		}
		equal(t, "reports", w.events(), []string{
			"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
		})
		equal(t, "artifacts", w.artifacts(), []string{
			"artifacts 4: observability cpu-INITIALIZING.pprof",
			"artifacts 5: artifact plan.json, artifact changes.json, workspace workspace",
			"artifacts 5: observability cpu-PLANNING.pprof",
		})
		for name, want := range map[string]map[string]int64{
			"cpu-INITIALIZING.pprof": {"init": 2},
			"cpu-PLANNING.pprof":     {"plan": 5, "showPlan": 1},
		} {
			if got := w.samples(name); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: %v, want %v", name, got, want)
			}
		}
		if trace := w.traced(); !slices.Contains(trace, "after_plan ") {
			t.Errorf("TOFU_CPU_PROFILE is set for a hook: %q", trace)
		}
	})
	t.Run("a task", func(t *testing.T) {
		w := newWorld(t)
		w.profiled(true, map[string]int64{"output": 3})
		w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"}
		w.h.Spec.Run.Type = wire.TASK
		w.h.Spec.Task = &wire.RunSpecTask{Command: "tofu output -json", SkipInitialization: true}
		if err := w.run(); err != nil {
			t.Fatal(err)
		}
		equal(
			t,
			"artifacts",
			w.artifacts(),
			[]string{"artifacts 5: observability cpu-PERFORMING.pprof"},
		)
		if got := w.samples("cpu-PERFORMING.pprof"); !reflect.DeepEqual(got,
			map[string]int64{"task": 3}) {
			t.Errorf("the task's profile: %v", got)
		}
	})
}

// TestUnitExecCPUProfilesBestEffort checks that a profile that does not
// parse, or whose upload is refused, is noted in the state's log and
// fails nothing, and that a tool that writes none, or a run that does not
// profile, uploads none.
func TestUnitExecCPUProfilesBestEffort(t *testing.T) {
	garbage := filepath.Join(t.TempDir(), "garbage")
	if err := os.WriteFile(garbage, []byte("not a profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		on        bool
		samples   map[string]int64
		env       []string
		refuse    wire.UploadKind
		artifacts []string
		notes     []string
	}{
		"a profile that does not parse": {
			on: true, samples: map[string]int64{"init": 1}, env: []string{"CPU_plan=" + garbage},
			artifacts: []string{
				"artifacts 4: observability cpu-INITIALIZING.pprof",
				"artifacts 5: artifact plan.json, artifact changes.json, workspace workspace",
			},
			notes: []string{"infraweave: the PLANNING CPU profile is not saved: the plan " +
				"command's profile: "},
		},
		"a refused upload": {
			on: true, samples: map[string]int64{"init": 1, "plan": 1},
			refuse: wire.UploadKindObservability,
			artifacts: []string{
				"artifacts 5: artifact plan.json, artifact changes.json, workspace workspace",
			},
			notes: []string{
				"infraweave: the INITIALIZING CPU profile is not saved: ",
				"infraweave: the PLANNING CPU profile is not saved: ",
			},
		},
		"a tool that writes none": {
			on: true,
			artifacts: []string{
				"artifacts 5: artifact plan.json, artifact changes.json, workspace workspace",
			},
		},
		"a run that does not profile": {
			samples: map[string]int64{"init": 1, "plan": 1},
			artifacts: []string{
				"artifacts 5: artifact plan.json, artifact changes.json, workspace workspace",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.profiled(c.on, c.samples)
			w.environ = append(w.environ, c.env...)
			w.w.refuseKind = c.refuse
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), []string{
				"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5",
			})
			equal(t, "artifacts", w.artifacts(), c.artifacts)
			out := w.out.String()
			for _, n := range c.notes {
				if !strings.Contains(out, n) {
					t.Errorf("no note %q in %s", n, out)
				}
			}
			if len(c.notes) == 0 && strings.Contains(out, "CPU profile") {
				t.Errorf("a note: %s", out)
			}
		})
	}
}
