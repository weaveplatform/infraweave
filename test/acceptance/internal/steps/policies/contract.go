package policies

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// Register installs assertions over the response from the served contract.
func Register(sc *godog.ScenarioContext, w *harness.World) {
	registerRuntime(sc, w)
	registerManagement(sc, w)
	registerAttachments(sc, w)
	registerAttachmentRaces(sc, w)
	registerCheck(sc, w)
	registerApproval(sc, w)
	registerPlan(sc, w)
	registerTrigger(sc, w)
	registerWorkflowFlags(sc, w)
	registerTask(sc, w)
	registerSampling(sc, w)
	registerSimulation(sc, w)
	registerEvidenceReaders(sc, w)
	registerCatalogue(sc, w)
	registerInitialization(sc, w)
	sc.Step(`^the policy contract validates every recorded input and decision$`, func() error { return validate(w.Last()) })
	sc.Step(`^the policy contract answer has the shared cache and rate headers$`, func() error {
		r := w.Last()
		if r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("X-Content-Type-Options") != "nosniff" || r.Header.Get("X-RateLimit-Limit") == "" ||
			r.Header.Get("X-RateLimit-Remaining") == "" ||
			r.Header.Get("X-RateLimit-Reset") == "" {
			return fmt.Errorf("%w: policy contract lacks shared headers: %v", harness.ErrStep, r.Header)
		}
		return nil
	})
}

func validate(r harness.Response) error {
	var contract struct {
		PolicyTypes map[string]struct {
			Input any `json:"input"`
			Rules any `json:"rules"`
		} `json:"policyTypes"`
	}
	if err := json.Unmarshal(r.Body, &contract); err != nil {
		return fmt.Errorf("decode served contract: %w", err)
	}
	if len(contract.PolicyTypes) != 10 {
		return fmt.Errorf("%w: served contract has %d policy types", harness.ErrStep, len(contract.PolicyTypes))
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(r.Body))
	if err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	const location = "https://infraweave.invalid/policy-contract.json"
	if err := c.AddResource(location, doc); err != nil {
		return fmt.Errorf("add served schema: %w", err)
	}
	files, err := filepath.Glob(filepath.Join("..", "fixtures", "policy-inputs", "*.json"))
	if err != nil {
		return fmt.Errorf("list policy fixtures: %w", err)
	}
	if len(files) != 23 {
		return fmt.Errorf("%w: %d policy fixtures, want 23", harness.ErrStep, len(files))
	}
	for _, file := range files {
		name := filepath.Base(file)
		policy := strings.ToUpper(strings.Split(name, "-")[0])
		if policy == "PUSH" {
			policy = "GIT_PUSH"
		}
		member := "input"
		if strings.HasSuffix(name, ".decision.json") {
			member = "rules"
		}
		schema, err := c.Compile(location + "#/policyTypes/" + policy + "/" + member)
		if err != nil {
			return fmt.Errorf("compile served %s %s: %w", policy, member, err)
		}
		body, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read fixture: %w", err)
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("decode fixture: %w", err)
		}
		if err := schema.Validate(namespace(value)); err != nil {
			return fmt.Errorf("served schema disagrees with %s: %w", name, err)
		}
	}
	return nil
}

func namespace(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			if k == "spacelift" {
				k = "infraweave"
			}
			out[k] = namespace(child)
		}
		return out
	case []any:
		for i := range v {
			v[i] = namespace(v[i])
		}
	}
	return value
}
