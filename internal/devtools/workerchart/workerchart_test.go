package workerchart_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/devtools/workerchart"
)

// chartDir is the repository's infraweave-workers chart.
var chartDir = filepath.Join("..", "..", "..", "deploy", "helm", "infraweave-workers")

func TestUnitWorkerChartSchemaIsCurrent(t *testing.T) {
	if err := workerchart.Check(chartDir); err != nil {
		t.Fatal(err)
	}
}

const crd = `---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
spec:
  versions:
  - name: v1alpha0
    storage: false
  - name: v1alpha1
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              size:
                x-kubernetes-int-or-string: true
                anyOf:
                - type: integer
                - type: string
                maxLength: 8
                x-kubernetes-validations:
                - rule: isQuantity(self)
              count:
                type: integer
                format: int32
                minimum: 0
              tags:
                type: array
                x-kubernetes-list-type: set
                items:
                  type: string
              limits:
                type: object
                additionalProperties:
                  type: string
              open:
                type: object
                additionalProperties: true
            required:
            - count
            x-kubernetes-validations:
            - rule: has(self.count)
`

func TestUnitPoolSpecClosesEveryNamedObject(t *testing.T) {
	got, err := workerchart.PoolSpec([]byte(crd))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"count"},
		"properties": map[string]any{
			"size":   map[string]any{"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "maxLength": float64(8)},
			"count":  map[string]any{"type": "integer", "minimum": float64(0)},
			"tags":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"limits": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"open":   map[string]any{"type": "object", "additionalProperties": true},
		},
	}
	if !reflect.DeepEqual(normalize(t, got), normalize(t, want)) {
		g, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("pool spec schema = %s", g)
	}
}

func normalize(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnitPoolSpecRefusesWhatItCannotConvert(t *testing.T) {
	for name, manifest := range map[string]string{
		"not YAML":          "spec: [",
		"no storage":        "spec:\n  versions:\n  - name: v1\n    storage: false\n",
		"no spec in schema": "spec:\n  versions:\n  - name: v1\n    storage: true\n    schema:\n      openAPIV3Schema:\n        type: object\n",
	} {
		if _, err := workerchart.PoolSpec([]byte(manifest)); !errors.Is(err, workerchart.ErrSchema) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, values := range map[string]string{"not JSON": "{", "no $defs": "{}"} {
		if _, err := workerchart.Render([]byte(values), []byte(crd)); !errors.Is(err, workerchart.ErrSchema) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := workerchart.Render([]byte(`{"$defs":{}}`), []byte("spec: [")); !errors.Is(err, workerchart.ErrSchema) {
		t.Errorf("a broken CRD: %v", err)
	}
}

func TestUnitWriteRefreshesAStaleSchema(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "crds"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := workerchart.Check(dir); !errors.Is(err, workerchart.ErrSchema) {
		t.Fatalf("a chart without its CRD = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, workerchart.CRDFile), []byte(crd), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workerchart.Write(dir); !errors.Is(err, workerchart.ErrSchema) {
		t.Fatalf("a chart without its schema = %v", err)
	}
	stale := `{"$defs": {"workerPoolSpec": {"type": "object"}}, "type": "object"}`
	if err := os.WriteFile(filepath.Join(dir, workerchart.SchemaFile), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workerchart.Check(dir); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("a stale schema = %v", err)
	}
	if err := workerchart.Write(dir); err != nil {
		t.Fatal(err)
	}
	if err := workerchart.Check(dir); err != nil {
		t.Fatalf("a written schema = %v", err)
	}
	if err := workerchart.Write(dir); err != nil {
		t.Fatalf("a current schema = %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, workerchart.SchemaFile), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workerchart.CRDFile), []byte(strings.Replace(crd, "minimum: 0", "minimum: 1", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if err := workerchart.Write(dir); !errors.Is(err, workerchart.ErrSchema) {
			t.Fatalf("an unwritable schema = %v", err)
		}
	}
}
