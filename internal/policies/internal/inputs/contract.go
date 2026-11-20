package inputs

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// KubernetesPlan is the vendor's raw object. Kubernetes API kinds and custom
// resources define their own keys, beyond the infraweave policy envelope.
type KubernetesPlan Object

// PlanCheck is a vendor-owned check result. The vendor defines its keys, as it
// does provider resource attributes; it is always an object.
type PlanCheck Object

// ContractDocument is the published contract: each policy type names its input
// and decision schema, sharing definitions for snapshots and vendor envelopes.
type ContractDocument struct {
	Schema      string                    `json:"$schema"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	PolicyTypes map[string]PolicyContract `json:"policyTypes"`
	Defs        map[string]*Schema        `json:"$defs"`
}

// PolicyContract describes one policy's input and raw rule output.
type PolicyContract struct {
	Input *Schema `json:"input"`
	Rules *Schema `json:"rules"`
}

// Schema is the JSON Schema vocabulary used by the policy contract. Schema
// dictionaries and JSON Schema's boolean-or-schema additionalProperties are
// metadata, separate from the typed policy input objects they describe.
type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Types                []string           `json:"type,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"`
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
	OneOf                []*Schema          `json:"oneOf,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
}

// Contract reflects the input and decision structs; no independently maintained
// JSON document can drift from the contract the server publishes.
func Contract() (ContractDocument, error) {
	c := ContractDocument{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		Title:       "infraweave Policy Contract",
		Description: "Policy input snapshots and raw decisions. Typed future phase fields are optional and are not synthesized.",
		PolicyTypes: map[string]PolicyContract{}, Defs: map[string]*Schema{},
	}
	pairs := map[string][2]reflect.Type{
		"ACCESS":         {reflect.TypeFor[AccessInput](), reflect.TypeFor[AccessDecision]()},
		"APPROVAL":       {reflect.TypeFor[ApprovalInput](), reflect.TypeFor[ApprovalDecision]()},
		"GIT_PUSH":       {reflect.TypeFor[PushInput](), reflect.TypeFor[PushDecision]()},
		"INITIALIZATION": {reflect.TypeFor[InitializationInput](), reflect.TypeFor[InitializationDecision]()},
		"INTENT":         {reflect.TypeFor[IntentInput](), reflect.TypeFor[IntentDecision]()},
		"LOGIN":          {reflect.TypeFor[LoginInput](), reflect.TypeFor[LoginDecision]()},
		"NOTIFICATION":   {reflect.TypeFor[NotificationInput](), reflect.TypeFor[NotificationDecision]()},
		"PLAN":           {reflect.TypeFor[PlanInput](), reflect.TypeFor[PlanDecision]()},
		"TASK":           {reflect.TypeFor[TaskInput](), reflect.TypeFor[TaskDecision]()},
		"TRIGGER":        {reflect.TypeFor[TriggerInput](), reflect.TypeFor[TriggerDecision]()},
	}
	for name, pair := range pairs {
		input, err := schemaOf(pair[0], c.Defs)
		if err != nil {
			return ContractDocument{}, err
		}
		rules, err := schemaOf(pair[1], c.Defs)
		if err != nil {
			return ContractDocument{}, err
		}
		c.PolicyTypes[name] = PolicyContract{Input: input, Rules: rules}
	}
	return c, nil
}

type field interface{ fieldType() reflect.Type }

func schemaOf(t reflect.Type, defs map[string]*Schema) (*Schema, error) {
	switch t {
	case reflect.TypeFor[json.RawMessage]():
		return &Schema{}, nil
	case reflect.TypeFor[SensitiveMask]():
		ref := &Schema{Ref: "#/$defs/SensitiveMask"}
		defs["SensitiveMask"] = &Schema{AnyOf: []*Schema{
			{Types: []string{"boolean"}},
			{Types: []string{"array"}, Items: ref},
			{Types: []string{"object"}, AdditionalProperties: ref},
		}}
		return ref, nil
	case reflect.TypeFor[PathStep]():
		return &Schema{AnyOf: []*Schema{{Types: []string{"string"}}, {Types: []string{"integer"}}}}, nil
	case reflect.TypeFor[Admin]():
		return &Schema{AnyOf: []*Schema{{Types: []string{"boolean"}}, {Types: []string{"string"}, Enum: []string{""}}}}, nil
	case reflect.TypeFor[RoleSet]():
		return &Schema{AnyOf: []*Schema{
			{Types: []string{"array"}, Items: &Schema{Types: []string{"string"}}},
			{Types: []string{"object"}, AdditionalProperties: &Schema{Types: []string{"boolean"}}},
		}}, nil
	case reflect.TypeFor[InProgress]():
		run, err := schemaOf(reflect.TypeFor[InProgressRun](), defs)
		if err != nil {
			return nil, err
		}
		return &Schema{AnyOf: []*Schema{{Types: []string{"array"}, Items: run}, {Types: []string{"string"}, Enum: []string{""}}}}, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		return schemaOf(t.Elem(), defs)
	case reflect.String:
		return &Schema{Types: []string{"string"}}, nil
	case reflect.Bool:
		return &Schema{Types: []string{"boolean"}}, nil
	case reflect.Int64:
		return &Schema{Types: []string{"integer"}}, nil
	case reflect.Float64:
		return &Schema{Types: []string{"number"}}, nil
	case reflect.Slice:
		items, err := schemaOf(t.Elem(), defs)
		if err != nil {
			return nil, err
		}
		return &Schema{Types: []string{"array"}, Items: items}, nil
	case reflect.Map:
		items, err := schemaOf(t.Elem(), defs)
		if err != nil {
			return nil, err
		}
		return &Schema{Types: []string{"object"}, AdditionalProperties: items}, nil
	case reflect.Struct:
		ref := &Schema{Ref: "#/$defs/" + t.Name()}
		if _, ok := defs[t.Name()]; ok {
			return ref, nil
		}
		out := &Schema{Types: []string{"object"}, Properties: map[string]*Schema{}, AdditionalProperties: false}
		defs[t.Name()] = out
		for i := range t.NumField() {
			f := t.Field(i)
			ft, ok := reflect.Zero(f.Type).Interface().(field)
			if !ok {
				return nil, fmt.Errorf("%w: %s.%s is not a presence field", errSchema, t.Name(), f.Name)
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			prop, err := schemaOf(ft.fieldType(), defs)
			if err != nil {
				return nil, err
			}
			tag := f.Tag.Get("contract")
			if strings.Contains(tag, "required") {
				out.Required = append(out.Required, name)
			}
			if _, enum, ok := strings.Cut(tag, "enum="); ok {
				prop = &Schema{AllOf: []*Schema{prop}, Enum: strings.Split(enum, ",")}
			}
			if strings.Contains(tag, "nullable") {
				prop = &Schema{AnyOf: []*Schema{prop, {Types: []string{"null"}}}}
			}
			out.Properties[name] = prop
		}
		constraints(t, out)
		return ref, nil
	}
	return nil, fmt.Errorf("%w: unsupported type %s", errSchema, t)
}

func exclusive(names ...[]string) []*Schema {
	out := make([]*Schema, 0, len(names))
	for _, group := range names {
		out = append(out, &Schema{Required: group})
	}
	return out
}

func constraints(t reflect.Type, s *Schema) {
	switch t {
	case reflect.TypeFor[AccessInput]():
		s.AllOf = []*Schema{
			{OneOf: exclusive([]string{"session"}, []string{"slack"})},
			{OneOf: exclusive([]string{"stack"}, []string{"module"})},
		}
	case reflect.TypeFor[LoginInput]():
		s.OneOf = exclusive([]string{"session"}, []string{"slack"})
	case reflect.TypeFor[PushInput](), reflect.TypeFor[TaskInput](), reflect.TypeFor[PlanContext]():
		s.OneOf = exclusive([]string{"stack"}, []string{"module"})
	case reflect.TypeFor[TriggerInput]():
		s.OneOf = exclusive([]string{"run", "stack"}, []string{"module", "stacks"})
	case reflect.TypeFor[NotificationInput]():
		s.OneOf = exclusive([]string{"internal_error"}, []string{"run_updated"}, []string{"module_version"}, []string{"worker_usage"})
	}
}

var errSchema = errors.New("invalid policy schema type")
