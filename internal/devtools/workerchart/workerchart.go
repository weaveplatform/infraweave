package workerchart

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// ErrSchema is a CRD or values schema that cannot be converted.
var ErrSchema = errors.New("worker chart schema")

// The chart files the schema is generated from and written to.
const (
	CRDFile    = "crds/workerpools.yaml"
	SchemaFile = "values.schema.json"
)

// Definition is the values schema $defs entry PoolSpec fills.
const Definition = "workerPoolSpec"

// kept are the JSON Schema keywords a CRD schema shares with it. A keyword
// outside it, such as format, nullable or an x-kubernetes extension, is
// the API server's to check.
var kept = map[string]bool{
	"type": true, "description": true, "enum": true, "default": true, "pattern": true,
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "minItems": true, "maxItems": true, "uniqueItems": true,
	"minProperties": true, "maxProperties": true, "required": true,
}

// PoolSpec is the JSON Schema of the WorkerPool spec in the CRD manifest
// crd, from its storage version.
func PoolSpec(crd []byte) (map[string]any, error) {
	var definition struct {
		Spec struct {
			Versions []struct {
				Storage bool `json:"storage"`
				Schema  struct {
					OpenAPIV3Schema map[string]any `json:"openAPIV3Schema"` //nolint:tagliatelle // the CRD's field
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(crd, &definition); err != nil {
		return nil, fmt.Errorf("%w: read the CRD: %w", ErrSchema, err)
	}
	for _, v := range definition.Spec.Versions {
		if !v.Storage {
			continue
		}
		properties, _ := v.Schema.OpenAPIV3Schema["properties"].(map[string]any)
		spec, ok := properties["spec"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: the CRD's storage version has no spec schema", ErrSchema)
		}
		return convert(spec), nil
	}
	return nil, fmt.Errorf("%w: the CRD has no storage version", ErrSchema)
}

// convert is in as JSON Schema: the shared keywords, its properties,
// items, additionalProperties and anyOf converted in turn, and closed to
// other properties when it names its own.
func convert(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		switch {
		case kept[k]:
			out[k] = v
		case k == "properties":
			properties := map[string]any{}
			for name, p := range v.(map[string]any) { //nolint:forcetypeassert // a CRD's properties are schemas
				properties[name] = convert(p.(map[string]any)) //nolint:forcetypeassert // a CRD's properties are schemas
			}
			out[k] = properties
		case k == "items" || k == "additionalProperties":
			if schema, ok := v.(map[string]any); ok {
				out[k] = convert(schema)
			} else {
				out[k] = v
			}
		case k == "anyOf":
			list := v.([]any) //nolint:forcetypeassert // a CRD's anyOf is a list of schemas
			schemas := make([]any, 0, len(list))
			for _, s := range list {
				schemas = append(schemas, convert(s.(map[string]any))) //nolint:forcetypeassert // a CRD's anyOf is a list of schemas
			}
			out[k] = schemas
		}
	}
	if _, named := in["properties"]; named {
		if _, open := in["additionalProperties"]; !open {
			out["additionalProperties"] = false
		}
	}
	return out
}

// Render is the values schema values with crd's WorkerPool spec as
// $defs.workerPoolSpec, its keys sorted and indented by two spaces.
func Render(values, crd []byte) ([]byte, error) {
	spec, err := PoolSpec(crd)
	if err != nil {
		return nil, err
	}
	var schema map[string]any
	if err := json.Unmarshal(values, &schema); err != nil {
		return nil, fmt.Errorf("%w: read the values schema: %w", ErrSchema, err)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: the values schema has no $defs", ErrSchema)
	}
	defs[Definition] = spec
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(schema); err != nil {
		return nil, fmt.Errorf("%w: write the values schema: %w", ErrSchema, err)
	}
	return b.Bytes(), nil
}

func render(chart string) (current, want []byte, err error) {
	crd, err := os.ReadFile(filepath.Join(chart, CRDFile))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	current, err = os.ReadFile(filepath.Join(chart, SchemaFile))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	want, err = Render(current, crd)
	return current, want, err
}

// Write rewrites the chart directory's values schema from its CRD.
func Write(chart string) error {
	current, want, err := render(chart)
	if err != nil {
		return err
	}
	if bytes.Equal(current, want) {
		return nil
	}
	if err := os.WriteFile(filepath.Join(chart, SchemaFile), want, 0o644); err != nil { //nolint:gosec // a committed source file
		return fmt.Errorf("%w: %w", ErrSchema, err)
	}
	return nil
}

// Check fails unless the chart directory's values schema is what Write
// writes.
func Check(chart string) error {
	current, want, err := render(chart)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, want) {
		return fmt.Errorf("%w: %s is stale; run make generate", ErrSchema, strings.TrimPrefix(filepath.Join(chart, SchemaFile), "./"))
	}
	return nil
}
