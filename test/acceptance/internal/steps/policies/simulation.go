package policies

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerSimulation(sc *godog.ScenarioContext, w *harness.World) {
	registerSimulationFixtures(sc, w)
	registerSimulationLimits(sc, w)
	var before map[string]string
	var objects map[string][32]byte
	sc.Step(`^the simulation's evaluation and workflow state is recorded$`, func(ctx context.Context) error {
		var err error
		before, err = simulationStorage(ctx, w)
		if err != nil {
			return err
		}
		objects, err = simulationObjects(ctx, w)
		return err
	})
	sc.Step(`^the simulation leaves evaluation, workflow and object storage unchanged$`, func(ctx context.Context) error {
		if before == nil {
			return fmt.Errorf("%w: simulation storage was not recorded", harness.ErrStep)
		}
		after, err := simulationStorage(ctx, w)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(before, after) {
			return fmt.Errorf("%w: simulation changed evaluation or workflow persistence", harness.ErrStep)
		}
		afterObjects, err := simulationObjects(ctx, w)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(objects, afterObjects) {
			return fmt.Errorf("%w: simulation changed actual object-store contents", harness.ErrStep)
		}
		return nil
	})
	sc.Step(`^the simulation decision is '(.*)'$`, func(want string) error {
		return simulationDecision(w.Last().Body, want)
	})
	sc.Step(`^the simulation keeps "([^"]+)" out of server logs$`, func(marker string) error {
		for _, server := range w.Servers {
			if strings.Contains(string(server.Stdout()), marker) || strings.Contains(string(server.Stderr()), marker) {
				return fmt.Errorf("%w: simulation leaked source/input/print into server logs", harness.ErrStep)
			}
		}
		return nil
	})
	sc.Step(`^token "(\w+)" still cannot create an administrative space$`, func(ctx context.Context, token string) error {
		response, err := w.Do(
			ctx,
			"B",
			token,
			"POST",
			"/api/v1/spaces",
			harness.Body(`{"name":"No simulated admin","description":"","inheritEntities":true,"parentSpace":"legacy"}`),
		)
		if err != nil {
			return err
		}
		if response.Status != 403 {
			return fmt.Errorf("%w: simulated LOGIN decision changed administrative permission: %d", harness.ErrStep, response.Status)
		}
		return nil
	})
}

func simulationObjects(ctx context.Context, w *harness.World) (map[string][32]byte, error) {
	store, err := w.Suite.Deps.ObjectStore(ctx)
	if err != nil {
		return nil, err
	}
	keys, _, err := store.Containing(ctx, w.Bucket, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string][32]byte, len(keys))
	for _, key := range keys {
		raw, err := store.Read(ctx, w.Bucket, key)
		if err != nil {
			return nil, err
		}
		out[key] = sha256.Sum256(raw)
	}
	return out, nil
}

func simulationDecision(body []byte, want string) error {
	var encoded string
	if err := json.Unmarshal(body, &encoded); err != nil {
		return fmt.Errorf("decode decision string: %w", err)
	}
	var got, expected any
	for _, target := range []struct {
		raw   string
		value *any
	}{{encoded, &got}, {want, &expected}} {
		decoder := json.NewDecoder(strings.NewReader(target.raw))
		decoder.UseNumber()
		if err := decoder.Decode(target.value); err != nil {
			return fmt.Errorf("decode full simulated decision: %w", err)
		}
	}
	if !reflect.DeepEqual(got, expected) {
		return fmt.Errorf("%w: simulated decision %s differs from %s", harness.ErrStep, encoded, want)
	}
	return nil
}

func simulationStorage(ctx context.Context, w *harness.World) (map[string]string, error) {
	out, err := policyStorage(ctx, w)
	if err != nil {
		return nil, err
	}
	for _, required := range []string{"policy_evaluations", "policy_sample_budgets", "policy_input_objects", "policy_input_capture_heads"} {
		if _, ok := out[required]; !ok {
			return nil, fmt.Errorf("%w: simulation cannot prove absent side effects without %s", harness.ErrStep, required)
		}
	}
	db, err := w.Querier(ctx)
	if err != nil {
		return nil, err
	}
	for _, table := range []string{"runs", "run_state_transitions", "run_reviews", "run_approval_gates", "stack_run_locks", "stack_run_state"} {
		rows, err := db.Query(ctx, `SELECT COALESCE(jsonb_agg(x ORDER BY x::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS x FROM `+table+` t) q`)
		if err != nil {
			return nil, fmt.Errorf("snapshot simulation workflow storage: %w", err)
		}
		out[table] = rows
	}
	return out, nil
}
