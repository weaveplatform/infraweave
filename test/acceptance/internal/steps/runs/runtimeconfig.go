package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// runtimeConfiguration compares the named public fields of a run's
// recorded configuration, resolving the scenario's release and run names.
func (r *runSteps) runtimeConfiguration(want string) error {
	return r.configurationFields("runtimeConfig", want)
}

// customRuntimeConfiguration checks the trigger's public input separately
// from the effective configuration fixed at handover.
func (r *runSteps) customRuntimeConfiguration(want string) error {
	return r.configurationFields("customRuntimeConfig", want)
}

func (r *runSteps) configurationFields(field, want string) error {
	var run map[string]json.RawMessage
	var got map[string]json.RawMessage
	var fields map[string]json.RawMessage
	last := r.w.Last()
	if last.Status != http.StatusOK ||
		json.Unmarshal(last.Body, &run) != nil || json.Unmarshal(run[field], &got) != nil || got == nil {
		return fmt.Errorf("%w: no %s: %d %s", harness.ErrStep, field, last.Status, last.Body)
	}
	if err := json.Unmarshal([]byte(r.w.Resolve(want)), &fields); err != nil {
		return fmt.Errorf("%w: expected runtime fields: %w", harness.ErrStep, err)
	}
	for name, raw := range fields {
		var actual, expected any
		if json.Unmarshal(raw, &expected) != nil || json.Unmarshal(got[name], &actual) != nil ||
			!reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("%w: runtime configuration's %s is %s, want %s", harness.ErrStep,
				name, got[name], raw)
		}
	}
	var environment []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	var rendered string
	var document struct {
		Environment map[string]string `yaml:"environment"`
	}
	if json.Unmarshal(got["environment"], &environment) != nil ||
		json.Unmarshal(got["yaml"], &rendered) != nil || rendered == "" ||
		yaml.Unmarshal([]byte(rendered), &document) != nil || len(document.Environment) != len(environment) {
		return fmt.Errorf("%w: %s has no matching JSON and YAML environment", harness.ErrStep, field)
	}
	for _, variable := range environment {
		value, exists := document.Environment[variable.Key]
		if variable.Value != "" || !exists || value != "" {
			return fmt.Errorf("%w: %s answers a value or omits the YAML key %s", harness.ErrStep, field, variable.Key)
		}
	}
	doc, _ := json.Marshal(got)
	fmt.Printf("acceptance: %s: %s\n", field, doc)
	return nil
}

// releaseEvidence records the actual version line from the log API, for
// the snapshot scenarios' plan/apply compatibility evidence.
func (r *runSteps) releaseEvidence(ctx context.Context, token, run, replica string) error {
	text, err := logText(ctx, r.w, token, run, "APPLYING", replica)
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Terraform v") || strings.HasPrefix(line, "OpenTofu v") {
			fmt.Printf("acceptance: APPLYING release: %s\n", line)
			return nil
		}
	}
	return fmt.Errorf("%w: the applying log names no tool release", harness.ErrStep)
}

// runtimeEvidence captures only the configuration probes and tool version
// from the actual planning-log API, leaving other run output out of the PR.
func (r *runSteps) runtimeEvidence(ctx context.Context, token, run, replica string) error {
	text, err := logText(ctx, r.w, token, run, "PLANNING", replica)
	if err != nil {
		return err
	}
	found := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hook: ") || strings.HasPrefix(line, "LEVEL=") ||
			strings.HasPrefix(line, "docs: ") || strings.HasPrefix(line, "Terraform v") ||
			strings.HasPrefix(line, "OpenTofu v") {
			fmt.Printf("acceptance: runtime %s PLANNING: %s\n", r.w.IDs[run], line)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: the planning log has no runtime configuration evidence", harness.ErrStep)
	}
	return nil
}

// retryEvidence records the removed hook's actual output from the retry's
// planning log, without printing unrelated lines from the run.
func (r *runSteps) retryEvidence(ctx context.Context, token, run, replica string) error {
	text, err := logText(ctx, r.w, token, run, "PLANNING", replica)
	if err != nil {
		return err
	}
	if strings.Contains(text, "hook: new") {
		return fmt.Errorf("%w: the retry runs the changed hook", harness.ErrStep)
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "hook: old" {
			fmt.Printf("acceptance: retry %s PLANNING log: %s\n", r.w.IDs[run], line)
			return nil
		}
	}
	return fmt.Errorf("%w: the retry's planning log has no removed-hook output", harness.ErrStep)
}
