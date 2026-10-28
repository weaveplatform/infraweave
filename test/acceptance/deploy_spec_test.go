package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/openapibundle"
)

// deploySrc is the deploy API's fragment directory, relative to this package.
var deploySrc = filepath.Join("..", "..", "api", "src", "deploy")

type deploySteps struct {
	doc *openapi3.T
	ref *openapi3.T
}

func (s *deploySteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the bundled deploy API$`, s.bundled)
	sc.Step(`^the bundle is up to date with api/src/deploy$`, s.upToDate)
	sc.Step(`^the deploy API validates as OpenAPI 3\.0\.3$`, s.validates)
	sc.Step(
		`^every reference operation tagged "([^"]+)" is in the deploy API with the same method, path and operationId$`,
		s.familyServed,
	)
	sc.Step(`^"GET ([^"]+)" is cursor-paginated with page schema "([^"]+)"$`, s.paginated)
	sc.Step(`^"([A-Z]+) ([^"]+)" accepts an Idempotency-Key$`, s.idempotent)
	sc.Step(`^"([A-Z]+) ([^"]+)" costs "([a-z]+)" and takes no Idempotency-Key$`, s.costsWithoutKey)
	sc.Step(
		`^every operation tagged "([^"]+)" can answer 429 and records its conventions$`,
		s.rateLimited,
	)
}

func (s *deploySteps) bundled() error {
	doc, err := openapi3.NewLoader().LoadFromData(api.Deploy)
	if err != nil {
		return fmt.Errorf("load deploy API: %w", err)
	}
	ref, err := loadReference()
	if err != nil {
		return err
	}
	s.doc, s.ref = doc, ref.doc
	return nil
}

func (s *deploySteps) upToDate() error {
	want, err := openapibundle.Bundle(deploySrc)
	if err != nil {
		return err
	}
	if !bytes.Equal(api.Deploy, want) {
		return fmt.Errorf("%w: api/deploy.openapi.yaml is stale; run make bundle", errStep)
	}
	return nil
}

func (s *deploySteps) validates() error {
	if s.doc.OpenAPI != openapibundle.Version {
		return fmt.Errorf("%w: openapi %s", errStep, s.doc.OpenAPI)
	}
	return s.doc.Validate(context.Background())
}

func (s *deploySteps) familyServed(tag string) error {
	var missing []string
	n := 0
	for path, item := range s.ref.Paths.Map() {
		for method, op := range item.Operations() {
			if !slices.Contains(op.Tags, tag) {
				continue
			}
			n++
			got := s.operation(method, path)
			if got == nil || got.OperationID != op.OperationID {
				missing = append(missing, method+" "+path+" "+op.OperationID)
			}
		}
	}
	if n == 0 {
		return fmt.Errorf("%w: the reference has no %s operations", errStep, tag)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("%w: %d of %d %s operations missing:\n%s", errStep, len(missing), n, tag,
			strings.Join(missing, "\n"))
	}
	return nil
}

func (s *deploySteps) operation(method, path string) *openapi3.Operation {
	item := s.doc.Paths.Value(path)
	if item == nil {
		return nil
	}
	return item.GetOperation(method)
}

func (s *deploySteps) mustOperation(method, path string) (*openapi3.Operation, error) {
	if op := s.operation(method, path); op != nil {
		return op, nil
	}
	return nil, fmt.Errorf("%w: no %s %s in the deploy API", errStep, method, path)
}

func paramRefs(op *openapi3.Operation) []string {
	var out []string
	for _, p := range op.Parameters {
		out = append(out, p.Ref)
	}
	return out
}

func (s *deploySteps) paginated(path, page string) error {
	op, err := s.mustOperation("GET", path)
	if err != nil {
		return err
	}
	refs := paramRefs(op)
	for _, want := range []string{"#/components/parameters/Cursor", "#/components/parameters/Limit"} {
		if !slices.Contains(refs, want) {
			return fmt.Errorf("%w: GET %s lacks %s", errStep, path, want)
		}
	}
	ok := op.Responses.Status(200)
	if ok == nil || ok.Value.Content.Get("application/json") == nil {
		return fmt.Errorf("%w: GET %s has no JSON 200", errStep, path)
	}
	if got := ok.Value.Content.Get(
		"application/json",
	).Schema.Ref; got != "#/components/schemas/"+page {
		return fmt.Errorf("%w: GET %s returns %q", errStep, path, got)
	}
	schema := s.doc.Components.Schemas[page]
	if schema == nil || schema.Value.Properties["items"] == nil ||
		schema.Value.Properties["nextCursor"] == nil {
		return fmt.Errorf("%w: %s is not an {items, nextCursor} envelope", errStep, page)
	}
	return nil
}

func (s *deploySteps) idempotent(method, path string) error {
	op, err := s.mustOperation(method, path)
	if err != nil {
		return err
	}
	if !slices.Contains(paramRefs(op), "#/components/parameters/IdempotencyKey") {
		return fmt.Errorf("%w: %s %s takes no Idempotency-Key", errStep, method, path)
	}
	for _, code := range []int{409, 422} {
		if op.Responses.Status(code) == nil {
			return fmt.Errorf("%w: %s %s documents no %d", errStep, method, path, code)
		}
	}
	return nil
}

func (s *deploySteps) costsWithoutKey(method, path, cost string) error {
	op, err := s.mustOperation(method, path)
	if err != nil {
		return err
	}
	if got := op.Extensions["x-infraweave-cost"]; got != cost {
		return fmt.Errorf("%w: %s %s costs %v", errStep, method, path, got)
	}
	if slices.Contains(paramRefs(op), "#/components/parameters/IdempotencyKey") {
		return fmt.Errorf("%w: %s %s takes an Idempotency-Key", errStep, method, path)
	}
	return nil
}

func (s *deploySteps) rateLimited(tag string) error {
	var bad []string
	for path, item := range s.doc.Paths.Map() {
		for method, op := range item.Operations() {
			if !slices.Contains(op.Tags, tag) {
				continue
			}
			div, _ := op.Extensions["x-infraweave-divergence"].([]any)
			if op.Responses.Status(429) == nil || !slices.Contains(div, any("rate-limit")) {
				bad = append(bad, method+" "+path)
			}
		}
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		return fmt.Errorf(
			"%w: no 429 or no recorded conventions:\n%s",
			errStep,
			strings.Join(bad, "\n"),
		)
	}
	return nil
}
