// Package specref turns the upstream Spacelift API (a GraphQL schema, the
// spacelift-api-bruno collection and the Spacelift Terraform provider) into a
// resource-oriented REST OpenAPI 3 reference for infraweave.
package specref

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// GraphQL type kinds as introspection reports them.
const (
	KindScalar      = "SCALAR"
	KindObject      = "OBJECT"
	KindInterface   = "INTERFACE"
	KindUnion       = "UNION"
	KindEnum        = "ENUM"
	KindInputObject = "INPUT_OBJECT"
	KindList        = "LIST"
	KindNonNull     = "NON_NULL"
)

var (
	ErrNoSchema        = errors.New("introspection response has no __schema")
	ErrIntrospection   = errors.New("introspection response reports errors")
	ErrUnknownRootType = errors.New("root operation type missing from schema")
)

// Introspection is the standard GraphQL introspection response envelope.
//
//nolint:tagliatelle // __schema is fixed by the GraphQL specification
type Introspection struct {
	Data struct {
		Schema *Schema `json:"__schema"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors,omitempty"`
}

type Schema struct {
	QueryType        *NamedRef   `json:"queryType"`
	MutationType     *NamedRef   `json:"mutationType"`
	SubscriptionType *NamedRef   `json:"subscriptionType"`
	Types            []*Type     `json:"types"`
	Directives       []Directive `json:"directives"`

	byName map[string]*Type
}

type NamedRef struct {
	Name string `json:"name"`
}

type Directive struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Locations   []string     `json:"locations"`
	Args        []InputValue `json:"args"`
}

type Type struct {
	Kind          string       `json:"kind"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	Fields        []Field      `json:"fields"`
	InputFields   []InputValue `json:"inputFields"`
	Interfaces    []TypeRef    `json:"interfaces"`
	EnumValues    []EnumValue  `json:"enumValues"`
	PossibleTypes []TypeRef    `json:"possibleTypes"`
}

type Field struct {
	Name              string       `json:"name"`
	Description       string       `json:"description"`
	Args              []InputValue `json:"args"`
	Type              TypeRef      `json:"type"`
	IsDeprecated      bool         `json:"isDeprecated"`
	DeprecationReason string       `json:"deprecationReason"`
}

type InputValue struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Type         TypeRef `json:"type"`
	DefaultValue *string `json:"defaultValue"`
}

type EnumValue struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	IsDeprecated      bool   `json:"isDeprecated"`
	DeprecationReason string `json:"deprecationReason"`
}

type TypeRef struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	OfType *TypeRef `json:"ofType"`
}

// NonNull reports whether the outermost wrapper is NON_NULL.
func (t TypeRef) NonNull() bool { return t.Kind == KindNonNull }

// Named strips every wrapper and returns the named type at the core.
func (t TypeRef) Named() TypeRef {
	for t.OfType != nil {
		t = *t.OfType
	}
	return t
}

// IsList reports whether the type, ignoring an outer NON_NULL, is a list.
func (t TypeRef) IsList() bool {
	if t.Kind == KindNonNull && t.OfType != nil {
		t = *t.OfType
	}
	return t.Kind == KindList
}

// Elem returns the element type of a (possibly non-null) list.
func (t TypeRef) Elem() TypeRef {
	if t.Kind == KindNonNull && t.OfType != nil {
		t = *t.OfType
	}
	if t.Kind == KindList && t.OfType != nil {
		return *t.OfType
	}
	return t
}

// String renders the type in SDL notation, e.g. [String!]!.
func (t TypeRef) String() string {
	switch t.Kind {
	case KindNonNull:
		return t.OfType.String() + "!"
	case KindList:
		return "[" + t.OfType.String() + "]"
	default:
		return t.Name
	}
}

// ParseIntrospection decodes an introspection response and indexes its types.
func ParseIntrospection(data []byte) (*Schema, error) {
	var in Introspection
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("decode introspection: %w", err)
	}
	if len(in.Errors) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrIntrospection, in.Errors[0])
	}
	s := in.Data.Schema
	if s == nil {
		return nil, ErrNoSchema
	}
	s.index()
	for _, root := range []*NamedRef{s.QueryType, s.MutationType} {
		if root != nil && s.Type(root.Name) == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownRootType, root.Name)
		}
	}
	return s, nil
}

// Normalize sorts types by name so a re-fetch of an unchanged schema yields
// byte-identical snapshots; field order is left as upstream declares it
// because it carries meaning for readers (identity fields first).
func (s *Schema) Normalize() {
	sort.Slice(s.Types, func(i, j int) bool { return s.Types[i].Name < s.Types[j].Name })
	sort.Slice(
		s.Directives,
		func(i, j int) bool { return s.Directives[i].Name < s.Directives[j].Name },
	)
	s.index()
}

func (s *Schema) index() {
	s.byName = make(map[string]*Type, len(s.Types))
	for _, t := range s.Types {
		s.byName[t.Name] = t
	}
}

// Type looks a named type up.
func (s *Schema) Type(name string) *Type { return s.byName[name] }

// Queries returns the root query fields, or nil when the schema has none.
func (s *Schema) Queries() []Field { return s.rootFields(s.QueryType) }

// Mutations returns the root mutation fields, or nil when the schema has none.
func (s *Schema) Mutations() []Field { return s.rootFields(s.MutationType) }

func (s *Schema) rootFields(ref *NamedRef) []Field {
	if ref == nil {
		return nil
	}
	return s.Type(ref.Name).Fields
}

// MarshalIntrospection renders the schema in the introspection envelope with
// stable indentation, the format committed as the reference snapshot.
func MarshalIntrospection(s *Schema) ([]byte, error) {
	var in Introspection
	in.Data.Schema = s
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode introspection: %w", err)
	}
	return append(b, '\n'), nil
}
