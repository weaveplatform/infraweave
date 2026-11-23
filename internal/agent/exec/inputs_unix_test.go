//go:build unix

package exec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/phaseinputs"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// probe appends name, the values of the variables, and whether each of
// files exists, to $TRACE.
func probe(name string, files []string, vars ...string) string {
	s := hook(name, vars...)
	for _, f := range files {
		s += `; if [ -e ` + f + ` ]; then printf '%s\n' "has ` + filepath.Base(f) + `: $(cat ` + f + `)" >> "$TRACE"; ` +
			`else printf '%s\n' "no ` + filepath.Base(f) + `" >> "$TRACE"; fi`
	}
	return s
}

// scoped gives w a read and a write scoped file in a directory of their
// own, and the variables TOKEN, ro_ and wo_, CACHE, unprefixed, and
// SHARED, which the stack's own variable overrides, and answers the
// files' paths.
func scoped(t *testing.T, w *world) (read, write string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The runner leaves the directory unwritable.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	read, write = filepath.Join(dir, "read.json"), filepath.Join(dir, "write.json")
	stack := "from-stack"
	w.h.Spec.Environment = []wire.RunSpecVariable{{Name: "SHARED", Value: &stack}}
	w.h.Inputs = phaseinputs.Inputs{
		Variables: []phaseinputs.Variable{
			{Name: "TOKEN", Exposure: phaseinputs.Read, Value: "read-token"},
			{Name: "TOKEN", Exposure: phaseinputs.Write, Value: "write-t0ken-secret", Sensitive: true},
			{Name: "CACHE", Exposure: phaseinputs.All, Value: "/cache"},
			{Name: "SHARED", Exposure: phaseinputs.Write, Value: "from-launcher"},
		},
		Files: []phaseinputs.File{
			{Target: read, Exposure: phaseinputs.Read, Content: []byte("reader")},
			{Target: write, Exposure: phaseinputs.Write, Content: []byte("writer-c0ntent"), Sensitive: true},
		},
	}
	return read, write
}

func TestUnitExecPhaseInputsFollowThePhases(t *testing.T) {
	w := newWorld(t)
	w.w.autodeploy = true
	read, write := scoped(t, w)
	files := []string{read, write}
	w.h.Spec.Hooks = map[string][]string{
		"before_init":  {probe("before_init", files, "TOKEN", "CACHE", "SHARED")},
		"before_plan":  {probe("before_plan", files, "TOKEN"), "echo \"plan $TOKEN\"", "echo x > " + read + " || echo refused"},
		"before_apply": {probe("before_apply", files, "TOKEN", "CACHE", "SHARED"), "echo \"apply $TOKEN\"", "cat " + write},
		"after_run":    {probe("after_run", files, "TOKEN")},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	equal(t, "reports", w.events(), []string{
		"INITIALIZING started 3", "INITIALIZING succeeded 4", "PLANNING succeeded 5", "APPLYING succeeded 6",
	})
	var probes []string
	for _, l := range w.traced() {
		if strings.HasPrefix(l, "before_") || strings.HasPrefix(l, "after_run") ||
			strings.HasPrefix(l, "has ") || strings.HasPrefix(l, "no ") {
			probes = append(probes, l)
		}
	}
	equal(t, "probes", probes, []string{
		"before_init read-token /cache from-stack", "has read.json: reader", "no write.json",
		"before_plan read-token", "has read.json: reader", "no write.json",
		"before_apply write-t0ken-secret /cache from-stack", "no read.json", "has write.json: writer-c0ntent",
		"after_run write-t0ken-secret", "no read.json", "has write.json: writer-c0ntent",
	})
	out := w.out.String()
	if !strings.Contains(out, "plan read-token") || !strings.Contains(out, "refused") ||
		strings.Contains(out, "write-t0ken-secret") || strings.Contains(out, "writer-c0ntent") {
		t.Errorf("output:\n%s", out)
	}
	if b, err := os.ReadFile(write); string(b) != "writer-c0ntent" || err != nil {
		t.Errorf("write.json: %q, %v", b, err)
	}
	if info, err := os.Stat(write); err != nil || info.Mode().Perm() != 0o400 {
		t.Errorf("write.json's mode: %v, %v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(write)); err != nil || info.Mode().Perm() != 0o500 {
		t.Errorf("the scoped directory's mode: %v, %v", info, err)
	}
}

func TestUnitExecPhaseInputsOfATaskAreWritePhases(t *testing.T) {
	w := newWorld(t)
	w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: string(wire.WorkerStatePERFORMING)}
	read, write := scoped(t, w)
	files := []string{read, write}
	w.h.Spec.Run.Type = wire.TASK
	w.h.Spec.Task = &wire.RunSpecTask{Command: probe("task", files, "TOKEN")}
	w.h.Spec.Hooks = map[string][]string{"before_init": {probe("before_init", files, "TOKEN")}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	var probes []string
	for _, l := range w.traced() {
		if !strings.HasPrefix(l, "init ") {
			probes = append(probes, l)
		}
	}
	equal(t, "probes", probes, []string{
		"before_init write-t0ken-secret", "no read.json", "has write.json: writer-c0ntent",
		"task write-t0ken-secret", "no read.json", "has write.json: writer-c0ntent",
	})
}

// TestUnitExecPhaseInputsAfterAnEndedSession checks that the after_run
// hooks of a run whose step ended the session see the variables of the
// phase before them in the new session.
func TestUnitExecPhaseInputsAfterAnEndedSession(t *testing.T) {
	w := newWorld(t)
	scoped(t, w)
	w.h.Spec.Hooks = map[string][]string{
		"before_plan": {"exit 3"},
		"after_run":   {hook("after_run", "TOKEN", "CACHE")},
	}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	trace := w.traced()
	if last := trace[len(trace)-1]; last != "after_run read-token /cache" {
		t.Errorf("trace: %q", trace)
	}
}

func TestUnitExecPhaseInputsFail(t *testing.T) {
	w := newWorld(t)
	w.h.Inputs.Files = []phaseinputs.File{{
		Target: filepath.Join(t.TempDir(), "missing", "key"), Exposure: phaseinputs.Read, Content: []byte("k"),
	}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	events := w.events()
	if len(events) != 2 || !strings.Contains(events[1], "INITIALIZING failed 4: The phase's scoped files cannot be exposed") {
		t.Errorf("reports: %q", events)
	}

	// A path the runner cannot remove fails the phase that must not see it.
	w = newWorld(t)
	w.w.autodeploy = true
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.MkdirAll(filepath.Join(dir, "read", "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	w.h.Inputs.Files = []phaseinputs.File{{
		Target: filepath.Join(dir, "read"), Exposure: phaseinputs.Write, Content: []byte("k"),
	}}
	if err := w.run(); err != nil {
		t.Fatal(err)
	}
	if events := w.events(); len(events) != 2 || !strings.Contains(events[1], "cannot be exposed") {
		t.Errorf("reports: %q", events)
	}
}
