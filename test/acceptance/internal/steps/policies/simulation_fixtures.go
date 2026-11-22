package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

type simulationExchange struct {
	Request struct {
		OperationName string `json:"operationName"`
		Variables     struct {
			Input api.PolicySimulateInput `json:"input"`
		} `json:"variables"`
	} `json:"request"`
	Response struct {
		Data struct {
			Decision string `json:"policySimulatev2"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	} `json:"response"`
}

func registerSimulationFixtures(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^recorded policy simulations "([^"]+)" agree through the public endpoint$`, func(ctx context.Context, name string) error {
		rows, err := simulationExchanges(name)
		if err != nil {
			return err
		}
		compared := 0
		for i, row := range rows {
			if name == "C273" && row.Request.OperationName != "SimulatePolicyV2" {
				continue
			}
			if row.Response.Data.Decision == "" && len(row.Response.Errors) == 0 {
				continue
			}
			in := row.Request.Variables.Input
			in.Body = strings.Replace(in.Body, "package spacelift", "package infraweave", 1)
			in.Body = strings.ReplaceAll(in.Body, "input.spacelift", "input.infraweave")
			in.Body = strings.ReplaceAll(in.Body, "data.spacelift", "data.infraweave")
			var root map[string]json.RawMessage
			if err := json.Unmarshal([]byte(in.Input), &root); err != nil {
				return fmt.Errorf("decode captured simulation input: %w", err)
			}
			if value, ok := root["spacelift"]; ok {
				root["infraweave"] = value
				delete(root, "spacelift")
				mapped, err := json.Marshal(root)
				if err != nil {
					return fmt.Errorf("map captured input namespace: %w", err)
				}
				in.Input = string(mapped)
			}
			replica := "A"
			if i%2 != 0 {
				replica = "B"
			}
			response, err := w.Do(ctx, replica, "h", "POST", "/api/v1/policies/simulate", in)
			if err != nil {
				return err
			}
			want := 200
			if len(row.Response.Errors) != 0 {
				want = 422
			}
			// ACCESS is an explicit policy-type deviation; it must remain
			// refused even though the upstream legacy runtime accepted it.
			if string(in.Type) == "ACCESS" {
				want = 400
			}
			if response.Status != want {
				return fmt.Errorf("%w: captured %s row %d returned %d, want %d", harness.ErrStep, name, i, response.Status, want)
			}
			if want == 200 {
				if err := simulationDecision(response.Body, row.Response.Data.Decision); err != nil {
					return fmt.Errorf("captured %s row %d: %w", name, i, err)
				}
			}
			compared++
		}
		if compared == 0 {
			return fmt.Errorf("%w: no recorded simulation executed", harness.ErrStep)
		}
		return nil
	})
}

func simulationExchanges(name string) ([]simulationExchange, error) {
	files := map[string]string{
		"preambles": "rego/preamble-simulations.json",
		"roles":     "rego/login-role-forms.json",
		"trigger":   "policy-inputs/provenance/trigger-defaults.json",
		"C273":      "captures/C273/response.json",
		"C192":      "captures/C192/response.json",
	}
	file, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown simulation recording", harness.ErrStep)
	}
	raw, err := os.ReadFile(filepath.Join(harness.RepoRoot, "test/fixtures", file))
	if err != nil {
		return nil, fmt.Errorf("read recorded simulation: %w", err)
	}
	var set struct {
		Exchanges []simulationExchange `json:"exchanges"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("decode recorded simulation: %w", err)
	}
	if len(set.Exchanges) != 0 {
		return set.Exchanges, nil
	}
	var single simulationExchange
	if name == "C192" {
		if err := json.Unmarshal(raw, &single.Response); err != nil {
			return nil, fmt.Errorf("decode recorded hash decision: %w", err)
		}
		request, err := os.ReadFile(filepath.Join(harness.RepoRoot, "test/fixtures/captures/C192/request.graphql"))
		if err != nil {
			return nil, fmt.Errorf("read recorded hash request: %w", err)
		}
		for line := range strings.SplitSeq(string(request), "\n") {
			if variables, ok := strings.CutPrefix(line, "# variables: "); ok {
				if err := json.Unmarshal([]byte(variables), &single.Request.Variables); err != nil {
					return nil, fmt.Errorf("decode recorded hash variables: %w", err)
				}
			}
		}
	} else if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("decode recorded trigger simulation: %w", err)
	}
	return []simulationExchange{single}, nil
}
