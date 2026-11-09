package expr

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

// schema is the part of a JSON Schema that decides a CEL type.
type schema struct {
	Type                 json.RawMessage    `json:"type"`
	Properties           map[string]*schema `json:"properties"`
	AdditionalProperties json.RawMessage    `json:"additionalProperties"`
	Items                *schema            `json:"items"`
}

// parseSchema decodes raw, an empty raw being the schema that allows
// anything.
func parseSchema(raw json.RawMessage) (*schema, error) {
	s := &schema{}
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, s); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return s, nil
}

// kind is the one JSON type s names, or "" when it names none or several.
// An object's properties name it an object without a type.
func (s *schema) kind() string {
	var one string
	if err := json.Unmarshal(s.Type, &one); err == nil {
		return one
	}
	if len(s.Type) == 0 && len(s.Properties) > 0 {
		return "object"
	}
	return ""
}

// open reports whether an object schema allows properties it does not
// name: additionalProperties is present and not false.
func (s *schema) open() bool {
	a := strings.TrimSpace(string(s.AdditionalProperties))
	return a != "" && a != "false"
}

// additional is the schema of an open object's other properties; true
// allows anything.
func (s *schema) additional() *schema {
	extra := &schema{}
	// true, or a schema that is not an object, allows anything.
	_ = json.Unmarshal(s.AdditionalProperties, extra)
	return extra
}

// provider declares the object types of a surface's variables. A value of
// one is a JSON object at run time, so a field is read as a map key; the
// checker alone knows the fields, and refuses a misspelt one.
//
// An object type's name is its variable's name, then each property on the
// way to it after a slash, as inputs/network/subnets[]. A slash cannot
// appear in an identifier, so no expression names the type itself.
type provider struct {
	*types.Registry
	objects map[string]map[string]*types.Type
}

func newProvider() (*provider, error) {
	reg, err := types.NewRegistry()
	if err != nil {
		return nil, fmt.Errorf("type registry: %w", err)
	}
	return &provider{Registry: reg, objects: map[string]map[string]*types.Type{}}, nil
}

// celType declares s, found at name, and returns its CEL type: an object
// with properties and no additionalProperties is an object type, any
// other object a map from string, an array a list, a JSON scalar its CEL
// scalar, and a schema of several types or none dyn.
func (p *provider) celType(name string, s *schema) *types.Type {
	switch s.kind() {
	case "string":
		return types.StringType
	case "integer":
		return types.IntType
	case "number":
		return types.DoubleType
	case "boolean":
		return types.BoolType
	case "null":
		return types.NullType
	case "array":
		if s.Items == nil {
			return types.NewListType(types.DynType)
		}
		return types.NewListType(p.celType(name+"[]", s.Items))
	case "object":
		if len(s.Properties) == 0 || s.open() {
			if !s.open() {
				return types.NewMapType(types.StringType, types.DynType)
			}
			return types.NewMapType(types.StringType, p.celType(name+"/*", s.additional()))
		}
		fields := make(map[string]*types.Type, len(s.Properties))
		for _, field := range slices.Sorted(maps.Keys(s.Properties)) {
			fields[field] = p.celType(name+"/"+field, s.Properties[field])
		}
		p.objects[name] = fields
		return types.NewObjectType(name)
	}
	return types.DynType
}

func (p *provider) FindStructType(name string) (*types.Type, bool) {
	if _, ok := p.objects[name]; ok {
		return types.NewTypeTypeWithParam(types.NewObjectType(name)), true
	}
	return p.Registry.FindStructType(name)
}

func (p *provider) FindStructFieldNames(name string) ([]string, bool) {
	if fields, ok := p.objects[name]; ok {
		return slices.Sorted(maps.Keys(fields)), true
	}
	return p.Registry.FindStructFieldNames(name)
}

// FindStructFieldType gives a field no accessors, so the interpreter reads
// it from the object's map.
func (p *provider) FindStructFieldType(name, field string) (*types.FieldType, bool) {
	if fields, ok := p.objects[name]; ok {
		t, ok := fields[field]
		if !ok {
			return nil, false
		}
		return &types.FieldType{Type: t}, true
	}
	return p.Registry.FindStructFieldType(name, field)
}

// NewValue refuses to build a declared object: they arrive as data.
func (p *provider) NewValue(name string, fields map[string]ref.Val) ref.Val {
	if _, ok := p.objects[name]; ok {
		return types.NewErr("type %s cannot be constructed", name)
	}
	return p.Registry.NewValue(name, fields)
}
