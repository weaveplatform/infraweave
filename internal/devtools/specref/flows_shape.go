package specref

import (
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

// Shape is a JSON value's type as a Flows client declares it: a Go struct of
// terraform-provider-flows or a TypeScript type of flowctl. It is what the
// snapshots record, smaller than a JSON Schema and stable to diff.
type Shape struct {
	// Type is string, integer, number, boolean, object, array or empty for
	// any value. A Ref shape has no Type.
	Type   string `json:"type,omitempty"`
	Format string `json:"format,omitempty"`
	// Ref names a shape in the snapshot's Types.
	Ref        string            `json:"ref,omitempty"`
	Nullable   bool              `json:"nullable,omitempty"`
	Enum       []string          `json:"enum,omitempty"`
	Items      *Shape            `json:"items,omitempty"`
	Properties map[string]*Shape `json:"properties,omitempty"`
	Required   []string          `json:"required,omitempty"`
	// Values is the shape of a map's values: Go map[string]T, TypeScript
	// Record<string, T>.
	Values *Shape `json:"values,omitempty"`
	// YAMLSchema names the overlay schema a string of YAML follows. The
	// mapping sets it; snapshots never record it.
	YAMLSchema string `json:"-"`
}

// RPC is one call a Flows client makes: POST <path> with a JSON body of
// Request, answered with Response in the envelope's data.
type RPC struct {
	Path     string `json:"path"`
	Request  *Shape `json:"request"`
	Response *Shape `json:"response"`
	// Unenveloped marks a response the client reads from the top level of
	// the body rather than from data, as flowctl reads /cli/projects.
	Unenveloped bool `json:"unenveloped,omitempty"`
}

// RPCSnapshot is one client's extracted RPC surface: provider-rpcs.json or
// flowctl-rpcs.json.
type RPCSnapshot struct {
	RPCs  []RPC             `json:"rpcs"`
	Types map[string]*Shape `json:"types,omitempty"`
}

func objectShape() *Shape { return &Shape{Type: "object", Properties: map[string]*Shape{}} }

func (s *Shape) require(name string) {
	s.Required = append(s.Required, name)
	sort.Strings(s.Required)
}

// isEmpty reports an object with no properties, Go's struct{}: the RPC
// answers nothing worth returning.
func (s *Shape) isEmpty() bool {
	return s == nil || (s.Type == "object" && len(s.Properties) == 0 && s.Values == nil)
}

// isScalar reports whether the shape fits a path or query parameter.
func (s *Shape) isScalar() bool {
	switch s.Type {
	case "string", "integer", "number", "boolean":
		return true
	}
	return false
}

// schema converts a shape to OpenAPI 3.0, a Ref becoming a component
// reference.
func (s *Shape) schema() *openapi3.SchemaRef {
	if s.Ref != "" {
		ref := componentRef(s.Ref)
		if !s.Nullable {
			return ref
		}
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Nullable: true,
			AllOf:    openapi3.SchemaRefs{ref},
		}}
	}
	out := &openapi3.Schema{Format: s.Format, Nullable: s.Nullable}
	if s.YAMLSchema != "" {
		out.Description = "YAML following the " + s.YAMLSchema + " schema."
		out.Extensions = map[string]any{"x-yaml-schema": componentRef(s.YAMLSchema).Ref}
	}
	if s.Type != "" {
		out.Type = &openapi3.Types{s.Type}
	}
	for _, e := range s.Enum {
		out.Enum = append(out.Enum, e)
	}
	if s.Items != nil {
		out.Items = s.Items.schema()
	}
	if s.Values != nil {
		out.AdditionalProperties = openapi3.AdditionalProperties{Schema: s.Values.schema()}
	}
	if len(s.Properties) > 0 {
		out.Properties = openapi3.Schemas{}
		for name, p := range s.Properties {
			out.Properties[name] = p.schema()
		}
		out.Required = append([]string(nil), s.Required...)
	}
	return out.NewRef()
}

// without is a copy of an object shape lacking the named properties, which a
// route carries in its path or query instead.
func (s *Shape) without(names map[string]bool) *Shape {
	out := *s
	out.Properties = map[string]*Shape{}
	out.Required = nil
	for name, p := range s.Properties {
		if !names[name] {
			out.Properties[name] = p
		}
	}
	for _, name := range s.Required {
		if !names[name] {
			out.Required = append(out.Required, name)
		}
	}
	return &out
}

// merged is the union of two request shapes that one operation answers, as
// the export operation answers both clients' requests. A property both
// declare must agree.
func merged(a, b *Shape) (*Shape, error) {
	if a == nil {
		return b, nil
	}
	out := *a
	out.Properties = map[string]*Shape{}
	for name, p := range a.Properties {
		out.Properties[name] = p
	}
	req := map[string]bool{}
	for _, name := range a.Required {
		req[name] = true
	}
	for name, p := range b.Properties {
		if q, ok := out.Properties[name]; ok && (q.Type != p.Type || q.Ref != p.Ref) {
			return nil, fmt.Errorf("%w: property %s is %s in one client and %s in the other",
				ErrFlowsSource, name, q.describe(), p.describe())
		}
		out.Properties[name] = p
	}
	// Required only where both clients always send it.
	out.Required = nil
	for _, name := range b.Required {
		if req[name] {
			out.Required = append(out.Required, name)
		}
	}
	sort.Strings(out.Required)
	return &out, nil
}

func (s *Shape) describe() string {
	if s.Ref != "" {
		return s.Ref
	}
	if s.Type == "" {
		return "any"
	}
	return s.Type
}
