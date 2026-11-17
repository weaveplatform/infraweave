package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// runtimeConfiguration compares the named public fields of a run's
// recorded configuration, resolving the scenario's release and run names.
func (r *runSteps) runtimeConfiguration(want string) error {
	var got struct {
		RuntimeConfig map[string]json.RawMessage `json:"runtimeConfig"`
	}
	var fields map[string]json.RawMessage
	last := r.w.Last()
	if last.Status != http.StatusOK || json.Unmarshal(last.Body, &got) != nil ||
		got.RuntimeConfig == nil {
		return fmt.Errorf("%w: no runtime configuration: %d %s", harness.ErrStep, last.Status, last.Body)
	}
	if err := json.Unmarshal([]byte(r.w.Resolve(want)), &fields); err != nil {
		return fmt.Errorf("%w: expected runtime fields: %w", harness.ErrStep, err)
	}
	for name, raw := range fields {
		var actual, expected any
		if json.Unmarshal(raw, &expected) != nil || json.Unmarshal(got.RuntimeConfig[name], &actual) != nil ||
			!reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("%w: runtime configuration's %s is %s, want %s", harness.ErrStep,
				name, got.RuntimeConfig[name], raw)
		}
	}
	// This step is used by the snapshot scenarios, whose public record
	// contains no environment values or private execution metadata.
	doc, _ := json.Marshal(got.RuntimeConfig)
	fmt.Printf("acceptance: runtime configuration: %s\n", doc)
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
