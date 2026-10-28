// Package acceptance runs the Gherkin features in features/ with godog.
package acceptance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/specref"
)

// referenceDir is the committed reference, relative to this package.
var referenceDir = filepath.Join("..", "..", "api", "reference", "spacelift")

var errStep = errors.New("step failed")

func TestAccFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) { (&referenceSteps{}).register(sc) },
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance features failed")
	}
}

type referenceSteps struct {
	src *specref.Sources
	gen *specref.Generated
	doc *openapi3.T
}

func (s *referenceSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the committed Spacelift reference$`, s.committedReference)
	sc.Step(`^the committed spec and coverage report are up to date$`, s.upToDate)
	sc.Step(`^the spec validates as OpenAPI 3\.0\.3$`, s.validates)
	sc.Step(
		`^every Spacelift query and mutation is routed or excluded with a reason$`,
		s.everyFieldAccounted,
	)
	sc.Step(
		`^no mutation is served as an RPC-shaped POST /<operation>$`,
		s.noRPC,
	)
	sc.Step(`^"([A-Z]+) ([^"]+)" stands for the GraphQL (query|mutation) "([^"]+)"$`, s.standsFor)
	sc.Step(`^"([A-Z]+) ([^"]+)" needs no bearer token$`, s.public)
	sc.Step(
		`^path parameter "([^"]+)" of "([A-Z]+) ([^"]+)" carries the GraphQL argument "([^"]+)"$`,
		s.pathArgument,
	)
	sc.Step(`^the body of "([A-Z]+) ([^"]+)" does not carry "([^"]+)"$`, s.bodyLacks)
	sc.Step(`^"([A-Z]+) ([^"]+)" needs a bearer token$`, s.secured)
	sc.Step(
		`^schema "([^"]+)" property "([^"]+)" is set by Terraform attribute "([^"]+)"$`,
		s.setByTerraform,
	)
	sc.Step(`^schema "([^"]+)" property "([^"]+)" has a description$`, s.hasDescription)
	sc.Step(`^schema "([^"]+)" property "([^"]+)" refers to "([^"]+)"$`, s.refersTo)
}

// loadReference generates from the committed sources once per test binary:
// the inputs are files under version control, so every scenario would get
// the same answer, and generation is the slow part of the suite.
var loadReference = sync.OnceValues(func() (*referenceSteps, error) {
	src, err := specref.ReadSources(referenceDir)
	if err != nil {
		return nil, err
	}
	gen, err := specref.Generate(context.Background(), src)
	if err != nil {
		return nil, err
	}
	// Steps about the spec read the embedded copy: it is what other code in
	// the module consumes.
	doc, err := openapi3.NewLoader().LoadFromData(api.SpaceliftReference)
	if err != nil {
		return nil, fmt.Errorf("load embedded reference: %w", err)
	}
	return &referenceSteps{src: src, gen: gen, doc: doc}, nil
})

func (s *referenceSteps) committedReference() error {
	ref, err := loadReference()
	if err != nil {
		return err
	}
	s.src, s.gen, s.doc = ref.src, ref.gen, ref.doc
	return nil
}

func (s *referenceSteps) upToDate(ctx context.Context) error {
	return specref.CheckDir(ctx, referenceDir)
}

func (s *referenceSteps) validates(ctx context.Context) error {
	if s.doc.OpenAPI != "3.0.3" {
		return fmt.Errorf("%w: openapi = %s", errStep, s.doc.OpenAPI)
	}
	return s.doc.Validate(ctx, openapi3.DisableExamplesValidation())
}

func (s *referenceSteps) everyFieldAccounted() error {
	accounted := map[string]bool{}
	for _, item := range s.doc.Paths.Map() {
		for _, op := range item.Operations() {
			if g, ok := op.Extensions["x-graphql"].(map[string]any); ok && g["type"] != nil &&
				(g["type"] == "Query" || g["type"] == "Mutation") {
				accounted[g["field"].(string)] = true
			}
		}
	}
	for _, e := range s.gen.Report.Excluded {
		if strings.TrimSpace(e.Reason) == "" {
			return fmt.Errorf("%w: %s excluded without a reason", errStep, e.Field)
		}
		accounted[e.Field] = true
	}
	var missing []string
	for _, f := range append(s.src.Schema.Queries(), s.src.Schema.Mutations()...) {
		if !accounted[f.Name] {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: not in the reference: %s", errStep, strings.Join(missing, ", "))
	}
	return nil
}

func (s *referenceSteps) noRPC() error {
	if rpc := s.gen.Report.RPC; len(rpc) > 0 {
		return fmt.Errorf("%w: RPC-shaped: %v", errStep, rpc)
	}
	return nil
}

func (s *referenceSteps) pathArgument(param, method, path, argument string) error {
	op, err := s.operation(method, path)
	if err != nil {
		return err
	}
	p := op.Parameters.GetByInAndName(openapi3.ParameterInPath, param)
	if p == nil {
		return fmt.Errorf("%w: %s %s has no path parameter %s", errStep, method, path, param)
	}
	if got := p.Extensions["x-graphql-arg"]; got != argument {
		return fmt.Errorf("%w: {%s} carries %v, want %s", errStep, param, got, argument)
	}
	return nil
}

// bodyLacks follows a dotted property path through the request body's
// component references and fails if its last segment is present.
func (s *referenceSteps) bodyLacks(method, path, dotted string) error {
	op, err := s.operation(method, path)
	if err != nil {
		return err
	}
	if op.RequestBody == nil {
		return fmt.Errorf("%w: %s %s has no request body", errStep, method, path)
	}
	schema := op.RequestBody.Value.Content.Get("application/json").Schema
	parts := strings.Split(dotted, ".")
	for i, name := range parts {
		if ref := strings.TrimPrefix(schema.Ref, "#/components/schemas/"); ref != "" {
			schema = s.doc.Components.Schemas[ref]
		}
		next, ok := schema.Value.Properties[name]
		if i == len(parts)-1 {
			if ok {
				return fmt.Errorf("%w: %s %s body carries %s", errStep, method, path, dotted)
			}
			return nil
		}
		if !ok {
			return fmt.Errorf("%w: %s %s body has no %s", errStep, method, path, name)
		}
		schema = next
	}
	return nil
}

func (s *referenceSteps) operation(method, path string) (*openapi3.Operation, error) {
	item := s.doc.Paths.Value(path)
	if item == nil {
		return nil, fmt.Errorf("%w: no path %s", errStep, path)
	}
	op := item.GetOperation(method)
	if op == nil {
		return nil, fmt.Errorf("%w: no %s on %s", errStep, method, path)
	}
	return op, nil
}

func (s *referenceSteps) standsFor(method, path, kind, field string) error {
	op, err := s.operation(method, path)
	if err != nil {
		return err
	}
	g, _ := op.Extensions["x-graphql"].(map[string]any)
	if g["operation"] != kind || g["field"] != field {
		return fmt.Errorf(
			"%w: %s %s stands for %v, want %s %s",
			errStep,
			method,
			path,
			g,
			kind,
			field,
		)
	}
	return nil
}

func (s *referenceSteps) public(method, path string) error {
	op, err := s.operation(method, path)
	if err != nil {
		return err
	}
	if op.Security == nil || len(*op.Security) != 0 {
		return fmt.Errorf("%w: %s %s requires authentication", errStep, method, path)
	}
	return nil
}

func (s *referenceSteps) secured(method, path string) error {
	op, err := s.operation(method, path)
	if err != nil {
		return err
	}
	if op.Security != nil || len(s.doc.Security) == 0 {
		return fmt.Errorf("%w: %s %s does not inherit bearer authentication", errStep, method, path)
	}
	return nil
}

func (s *referenceSteps) property(schema, prop string) (*openapi3.SchemaRef, error) {
	sc := s.doc.Components.Schemas[schema]
	if sc == nil {
		return nil, fmt.Errorf("%w: no schema %s", errStep, schema)
	}
	p := sc.Value.Properties[prop]
	if p == nil {
		return nil, fmt.Errorf("%w: %s has no property %s", errStep, schema, prop)
	}
	return p, nil
}

func (s *referenceSteps) setByTerraform(schema, prop, attr string) error {
	p, err := s.property(schema, prop)
	if err != nil {
		return err
	}
	names, _ := p.Value.Extensions["x-terraform"].([]any)
	if !slices.Contains(names, any(attr)) {
		return fmt.Errorf("%w: %s.%s x-terraform = %v, want %s", errStep, schema, prop, names, attr)
	}
	return nil
}

func (s *referenceSteps) hasDescription(schema, prop string) error {
	p, err := s.property(schema, prop)
	if err != nil {
		return err
	}
	if strings.TrimSpace(p.Value.Description) == "" {
		return fmt.Errorf("%w: %s.%s has no description", errStep, schema, prop)
	}
	return nil
}

func (s *referenceSteps) refersTo(schema, prop, target string) error {
	p, err := s.property(schema, prop)
	if err != nil {
		return err
	}
	if p.Ref != "#/components/schemas/"+target {
		return fmt.Errorf("%w: %s.%s refers to %q, want %s", errStep, schema, prop, p.Ref, target)
	}
	return nil
}
