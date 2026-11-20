package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerCheck(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the policy compiler accepts at most (\d+) source bytes$`, func(limit int) {
		w.Settings["policy"] = map[string]any{"max_body_bytes": limit}
	})
	sc.Step(`^policy check '(.*)' through ([AB]) agrees with save through ([AB])$`, func(ctx context.Context, raw, checkReplica, saveReplica string) error {
		var input api.PolicyCheckRequest
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			return fmt.Errorf("decode check fixture: %w", err)
		}
		before, err := policyStorage(ctx, w)
		if err != nil {
			return err
		}
		checked, err := w.Do(ctx, checkReplica, "h", "POST", "/api/v1/policies/check", input)
		if err != nil {
			return err //nolint:wrapcheck // harness names the request
		}
		var check api.PolicyCheck
		if checked.Status != 200 || json.Unmarshal(checked.Body, &check) != nil || check.Diagnostics == nil {
			return fmt.Errorf("%w: check failed: %d %s", harness.ErrStep, checked.Status, checked.Body)
		}
		after, err := policyStorage(ctx, w)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("%w: check mutated a policy table or artifact index", harness.ErrStep)
		}
		saved, err := w.Do(
			ctx,
			saveReplica,
			"h",
			"POST",
			"/api/v1/policies",
			api.PolicyCreateInput{Name: "Checked policy", Type: input.Type, Body: input.Body, EngineType: input.EngineType},
		)
		if err != nil {
			return err //nolint:wrapcheck // harness names the request
		}
		if err := sameCompilation(check, saved, 201); err != nil {
			return err
		}
		baseline, err := w.Do(
			ctx,
			saveReplica,
			"h",
			"POST",
			"/api/v1/policies",
			api.PolicyCreateInput{Name: "Update target", Type: input.Type, Body: "package infraweave\n", EngineType: input.EngineType},
		)
		if err != nil {
			return err //nolint:wrapcheck // harness names the request
		}
		var target struct {
			ID string `json:"id"`
		}
		if baseline.Status != 201 || json.Unmarshal(baseline.Body, &target) != nil || target.ID == "" {
			return fmt.Errorf("%w: update baseline failed: %d %s", harness.ErrStep, baseline.Status, baseline.Body)
		}
		updated, err := w.Do(
			ctx,
			saveReplica,
			"h",
			"PUT",
			"/api/v1/policies/"+target.ID,
			api.PolicyUpdateInput{Name: "Updated policy", Body: input.Body, EngineType: input.EngineType},
		)
		if err != nil {
			return err //nolint:wrapcheck // harness names the request
		}
		return sameCompilation(check, updated, 200)
	})
}

func sameCompilation(check api.PolicyCheck, saved harness.Response, valid int) error {
	if len(check.Diagnostics) == 0 {
		if saved.Status != valid {
			return fmt.Errorf("%w: valid check/save disagree: %d %s", harness.ErrStep, saved.Status, saved.Body)
		}
		return nil
	}
	var refusal struct {
		Errors []api.ProblemFieldError `json:"errors"`
	}
	if saved.Status != 422 || json.Unmarshal(saved.Body, &refusal) != nil || !reflect.DeepEqual(refusal.Errors, check.Diagnostics) {
		return fmt.Errorf("%w: save/check diagnostics disagree: %+v; %s", harness.ErrStep, check.Diagnostics, saved.Body)
	}
	return nil
}

// Include current policy tables and the declared future run receipt/flag tables.
// An absent future relation is not evidence that its persistence has been tested.
func policyStorage(ctx context.Context, w *harness.World) (map[string]string, error) {
	db, err := w.Querier(ctx)
	if err != nil {
		return nil, fmt.Errorf("check policy storage: %w", err)
	}
	tables, err := db.Query(
		ctx,
		`SELECT tablename FROM pg_tables WHERE schemaname='public' AND (tablename IN ('policies','blob_objects','run_policy_receipts','run_flags') OR tablename LIKE 'policy\_%' ESCAPE '\') ORDER BY tablename`,
	)
	if err != nil {
		return nil, fmt.Errorf("list policy storage: %w", err)
	}
	out := map[string]string{}
	for _, name := range strings.Fields(tables) {
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		rows, err := db.Query(ctx, `SELECT COALESCE(jsonb_agg(x ORDER BY x::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS x FROM `+quoted+` t) q`)
		if err != nil {
			return nil, fmt.Errorf("snapshot policy storage: %w", err)
		}
		out[name] = rows
	}
	if _, ok := out["policies"]; !ok {
		return nil, fmt.Errorf("%w: policies storage not installed", harness.ErrStep)
	}
	return out, nil
}
