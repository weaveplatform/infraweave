package runs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/policies/policytest"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerSanitization(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the relay refuses plan uploads$`, func() error {
		if w.Relay == nil {
			return fmt.Errorf("%w: no launcher relay", harness.ErrStep)
		}
		w.Relay.RefusePlanUploads()
		return nil
	})
	sc.Step(
		`^a raw plan is sealed and uploaded without recording for the run "(\w+)" using token "(\w+)" and artifact key "(\w+)" through ([AB])$`,
		func(ctx context.Context, run, token, key, replica string) error {
			id, err := kernel.ParseULID(w.IDs[run])
			if err != nil {
				return err
			}
			artifact, err := hex.DecodeString(w.IDs[key])
			if err != nil {
				return err
			}
			account, err := kernel.ParseAccountID(harness.Home.Account)
			if err != nil {
				return err
			}
			return w.UploadArtifact(
				ctx,
				replica,
				token,
				account,
				id,
				kernel.RunStatePlanning,
				5,
				"plan.json",
				artifact,
				[]byte(`{"format_version":"1.2","variables":{"secret":{"value":"dummy-plan-secret-642"}}}`),
			)
		},
	)

	sc.Step(
		`^the opened worker plan of the run "(\w+)" and its "(terraform|opentofu|terragrunt)" policy projection use "(DEFAULT|SMART|NONE)" with units "([^"]*)"$`,
		func(ctx context.Context, run, vendor, mode, paths string) error {
			plain, stored, err := w.ReadPlanArtifact(ctx, run)
			if err != nil {
				return err
			}
			const secret = "dummy-plan-secret-642"
			if !bytes.HasPrefix(stored, []byte("IWSB")) || bytes.Contains(stored, []byte(secret)) {
				return fmt.Errorf("%w: plan is not sealed", harness.ErrStep)
			}
			if !json.Valid(plain) || bytes.Contains(plain, []byte(secret)) != (mode == "NONE") {
				return fmt.Errorf("%w: opened worker plan has incorrect sensitive value preservation for %s", harness.ErrStep, mode)
			}
			if bytes.Contains(plain, []byte("visible-642")) != (mode != "DEFAULT") {
				return fmt.Errorf("%w: opened worker plan has incorrect public value preservation", harness.ErrStep)
			}
			if mode != "NONE" && !bytes.Contains(plain, []byte(sanitize.Value(secret).(string))) {
				return fmt.Errorf("%w: worker did not use the shared sanitized hash", harness.ErrStep)
			}
			projected, err := policytest.PlanVendor(vendor, bytes.NewReader(plain))
			if err != nil {
				return err
			}
			if bytes.Contains(projected, []byte(secret)) != (mode == "NONE") {
				return fmt.Errorf("%w: policy projection sensitive values differ", harness.ErrStep)
			}
			var input map[string]json.RawMessage
			if err := json.Unmarshal(projected, &input); err != nil {
				return err
			}
			if vendor != "terragrunt" {
				if paths != "" {
					return fmt.Errorf("%w: unexpected unit assertion", harness.ErrStep)
				}
				if err := samePlanSections(plain, input["terraform"], "terraform_version"); err != nil {
					return err
				}
				if vendor == "opentofu" {
					return samePlanSections(plain, input["opentofu"], "opentofu_version")
				}
				return nil
			}
			var envelope struct {
				Units []struct {
					Path string          `json:"path"`
					Plan json.RawMessage `json:"plan"`
				} `json:"units"`
			}
			if err := json.Unmarshal(plain, &envelope); err != nil {
				return err
			}
			var units map[string]json.RawMessage
			if err := json.Unmarshal(input["terragrunt"], &units); err != nil {
				return err
			}
			var got []string
			for _, unit := range envelope.Units {
				got = append(got, unit.Path)
				if err := samePlanSections(unit.Plan, units[unit.Path], "terraform_version"); err != nil {
					return err
				}
			}
			slices.Sort(got)
			want := strings.Split(paths, ", ")
			slices.Sort(want)
			if !slices.Equal(got, want) || len(units) != len(want) {
				return fmt.Errorf("%w: selected unit paths %v, want %v", harness.ErrStep, got, want)
			}
			return nil
		},
	)
	sc.Step(
		`^the opened worker plan of the run "(\w+)" retains "([a-z_]+)" as '([^']+)'$`,
		func(ctx context.Context, run, family, expected string) error {
			plain, _, err := w.ReadPlanArtifact(ctx, run)
			if err != nil {
				return err
			}
			var plan map[string]json.RawMessage
			if err := json.Unmarshal(plain, &plan); err != nil {
				return err
			}
			var got, want any
			if err := json.Unmarshal(plan[family], &got); err != nil {
				return fmt.Errorf("%w: opened plan is missing its payload family", harness.ErrStep)
			}
			if err := json.Unmarshal([]byte(expected), &want); err != nil {
				return fmt.Errorf("%w: invalid expected payload family", harness.ErrStep)
			}
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("%w: opened plan changed its explicit opt-out payload", harness.ErrStep)
			}
			return nil
		},
	)
	sc.Step(`^the run "(\w+)" recorded (\d+) worker plan artifacts$`, func(ctx context.Context, run string, want int) error {
		var count int
		if err := w.Row(
			ctx,
			`SELECT count(*) FROM run_artifacts WHERE account_id=$1 AND run_id=$2 AND kind='artifact' AND name='plan.json'`,
			[]any{harness.UUIDOf(harness.Home.Account), harness.UUIDOf(w.IDs[run])},
			&count,
		); err != nil {
			return err
		}
		if count != want {
			return fmt.Errorf("%w: recorded %d plan artifacts, want %d", harness.ErrStep, count, want)
		}
		return nil
	})
}

// Compare full tool-written resource changes, checks and version, including
// absent/null and numeric types. No projection is allowed to omit a change key.
func samePlanSections(document, projected []byte, versionKey string) error {
	var raw, block map[string]json.RawMessage
	if err := json.Unmarshal(document, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(projected, &block); err != nil {
		return err
	}
	for _, key := range []string{"resource_changes", "checks", "terraform_version"} {
		target := key
		if key == "terraform_version" {
			target = versionKey
		}
		var want, got any
		if value, ok := raw[key]; ok {
			if err := json.Unmarshal(value, &want); err != nil {
				return err
			}
		}
		if value, ok := block[target]; ok {
			if err := json.Unmarshal(value, &got); err != nil {
				return err
			}
		}
		_, present := raw[key]
		_, projectedPresent := block[target]
		if present != projectedPresent || !reflect.DeepEqual(want, got) {
			return fmt.Errorf("%w: policy projection changed the plan's %s", harness.ErrStep, key)
		}
	}
	return nil
}
