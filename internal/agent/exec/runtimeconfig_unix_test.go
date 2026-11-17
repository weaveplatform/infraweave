//go:build !windows

package exec_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

func TestUnitExecRuntimeNotices(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "skip init"}[skip], func(t *testing.T) {
			w := newWorld(t)
			notices := []string{"unknown key stacks.infra.befor_plan"}
			w.h.Spec.Notices = &notices
			w.h.Spec.Run.Type = wire.TASK
			w.h.Spec.Task = &wire.RunSpecTask{Command: hook("task", "TF_VAR_infraweave_run_state"), SkipInitialization: skip}
			w.h.Spec.Hooks = map[string][]string{"before_init": {hook("before")}}
			w.w.then = map[wire.WorkerState]string{wire.WorkerStateINITIALIZING: "PERFORMING"}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			if strings.Count(w.out.String(), "infraweave: .infraweave/config.yml: "+notices[0]) != 1 ||
				!slices.Contains(w.traced(), "task PERFORMING") || slices.Contains(w.traced(), "before") == skip {
				t.Fatalf("notice changed execution: trace %v, log %s", w.traced(), w.out.String())
			}
			if skip && !strings.Contains(w.out.String(), "initialization is skipped") {
				t.Fatal("skip log missing after notice")
			}
		})
	}
}

func TestUnitExecRuntimeRefusal(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "skip init task"}[skip], func(t *testing.T) {
			w := newWorld(t)
			refusal := "stacks.infra.before_plan must be a list of strings"
			notices := []string{"unknown key stacks.infra.befor_plan"}
			w.h.Spec.Refusal, w.h.Spec.Notices = &refusal, &notices
			w.h.Spec.Hooks = map[string][]string{
				"before_init": {hook("before")}, "after_init": {hook("after")}, "after_run": {hook("final")},
			}
			if skip {
				w.h.Spec.Run.Type = wire.TASK
				w.h.Spec.Task = &wire.RunSpecTask{Command: hook("task"), SkipInitialization: true}
			}
			if err := w.run(); err != nil {
				t.Fatal(err)
			}
			equal(t, "reports", w.events(), []string{"INITIALIZING started 3", "INITIALIZING failed 4: " + refusal})
			if trace := w.traced(); len(trace) != 0 {
				t.Fatalf("refusal executed commands: %v", trace)
			}
			for _, want := range append(notices, refusal) {
				if !strings.Contains(w.out.String(), "infraweave: .infraweave/config.yml: "+want) {
					t.Fatalf("missing runtime diagnostic %q in %s", want, w.out.String())
				}
			}
			if strings.Contains(w.out.String(), "initialization is skipped") {
				t.Fatal("skip handling preceded the refusal")
			}
		})
	}
}
