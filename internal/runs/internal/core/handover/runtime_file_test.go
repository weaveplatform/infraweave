package handover_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/runtimeconfig"
)

func TestUnitRepositoryHandoverRecord(t *testing.T) {
	w := newWorld(t)
	w.sources.file = []byte(`version: '2'
stack_defaults:
  environment: {LEVEL: defaults, FILE_SECRET: file-secret-567}
  runner_image: runner:defaults
stacks:
  infra:
    before_plan: [file-hook]
    project_root: file-root
    diagnostic: retained
`)
	w.store.run.RuntimeConfig = storedRuntime(t, "terraform", json.RawMessage(`{"projectRoot":"custom-root","beforePlan":["custom-hook"],"environment":[{"key":"LEVEL","value":"custom"}]}`))
	w.store.overrides = []lifecycle.Override{{Name: "LEVEL", Value: "custom"}}
	w.env.resolved.Elements = []configapi.Element{variable("LEVEL", "stack", false), variable("FILE_SECRET", "stack-secret", true)}
	h, err := w.firstHandover(t, w.run.ID)
	if err != nil || *h.Spec.Stack.ProjectRoot != "custom-root" || *h.Spec.RunnerImage != "runner:defaults" ||
		!slices.Equal(w.env.hooks["before_plan"], []string{"custom-hook"}) ||
		!slices.Equal(w.sources.fileAsked, []string{"abc " + runtimeconfig.FilePath}) {
		t.Fatalf("first repository handover: %+v, %v", h.Spec, err)
	}
	var sec sealed
	if err := json.Unmarshal(h.Secrets.Reveal(), &sec); err != nil || sec.Environment["FILE_SECRET"] != "file-secret-567" {
		t.Fatalf("file value lost write-only delivery: %+v, %v", sec.Environment, err)
	}
	for _, v := range h.Spec.Environment {
		if v.Name == "LEVEL" && (v.Value == nil || *v.Value != "custom") {
			t.Fatalf("file beat custom variable: %+v", v)
		}
	}
	record := w.store.run.ResolvedConfig
	doc, _ := json.Marshal(record)
	if record == nil || bytes.Contains(doc, []byte("file-secret-567")) || !strings.Contains(record.Configuration.YAML, "diagnostic: retained") ||
		strings.Contains(record.Configuration.YAML, "stack_defaults") || len(record.Execution.Notices) != 1 {
		t.Fatalf("unsafe or incomplete repository record: %s", doc)
	}
	w.sources.fileErr = errDown
	w.stacks.settings.RunnerImage = new("changed")
	w.stacks.settings.ProjectRoot = new("changed")
	h, err = w.handover(t, w.run.ID)
	if err != nil || len(w.sources.fileAsked) != 1 || *h.Spec.Stack.ProjectRoot != "custom-root" || *h.Spec.RunnerImage != "runner:defaults" ||
		len(h.Spec.Notices) != 1 {
		t.Fatalf("record reread repository/settings: %+v, %v", h.Spec, err)
	}
	if err := json.Unmarshal(h.Secrets.Reveal(), &sec); err != nil || sec.Environment["FILE_SECRET"] != "file-secret-567" {
		t.Fatalf("replay lost sealed file value: %+v, %v", sec.Environment, err)
	}
}

func TestUnitRepositoryHandoverIgnores(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, ignore bool
	}{
		{"label", false, true}, {"local archive", true, false}, {"missing", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			if tc.ignore {
				w.stacks.labels = []string{"feature:ignore_runtime_config"}
				w.sources.fileErr = errDown
			}
			if tc.local {
				w.store.run.LocalWorkspace = "acct/stack/upload"
				w.sources.fileErr = errDown
			}
			h, err := w.firstHandover(t, w.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantReads := 1
			if tc.ignore || tc.local {
				wantReads = 0
			}
			if len(w.sources.fileAsked) != wantReads || tc.local && len(h.Spec.Notices) != 1 {
				t.Fatalf("ignored file read/report: %+v, reads %v", h.Spec.Notices, w.sources.fileAsked)
			}
		})
	}
}

func TestUnitRepositoryHandoverRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		file []byte
		err  error
	}{
		{"type", []byte("stacks: {infra: {before_plan: wrong}}"), nil},
		{"bound", nil, runsapi.ErrFileTooLarge},
		{"provider", nil, errDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.sources.file, w.sources.fileErr = tc.file, tc.err
			w.store.run.RuntimeConfig = storedRuntime(t, "terraform", json.RawMessage(`{"projectRoot":"custom-root","runnerImage":"runner:custom"}`))
			h, err := w.firstHandover(t, w.run.ID)
			if tc.name == "provider" {
				if !errors.Is(err, errDown) || w.store.run.ResolvedConfig != nil {
					t.Fatalf("provider error became recorded refusal: %v", err)
				}
				return
			}
			if err != nil || h.Spec.Refusal == nil || *h.Spec.Stack.ProjectRoot != "stack-root" || *h.Spec.RunnerImage != "ghcr.io/acme/stack" ||
				w.store.run.ResolvedConfig.Execution.Refusal == nil {
				t.Fatalf("file refusal did not retain base spec: %+v, %v", h.Spec, err)
			}
			if tc.name == "type" && !strings.Contains(*h.Spec.Refusal, "stacks.infra.before_plan") {
				t.Fatal(*h.Spec.Refusal)
			}
		})
	}
}
