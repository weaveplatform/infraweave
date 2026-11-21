package sanitize_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

func TestUnitModernPlanFixtures(t *testing.T) {
	const root = "../../../test/fixtures/plan-payloads"
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Plans map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "plan.json"))
	if err != nil || len(files) != 5 || len(manifest.Plans) != len(files) {
		t.Fatalf("modern fixture inventory: %d, %v", len(files), err)
	}
	for _, file := range files {
		name := filepath.Base(filepath.Dir(file))
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != manifest.Plans[name].SHA256 {
				t.Fatal("recorded document bytes differ from provenance")
			}
			for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
				t.Run(string(mode), func(t *testing.T) {
					var out bytes.Buffer
					if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
						t.Fatal(err)
					}
					if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
						t.Fatal("NONE changed captured bytes")
					}
					if bytes.Contains(out.Bytes(), []byte("dummy-action-secret-675")) != (mode == sanitize.None) {
						t.Fatal("recorded sensitive strings or copies escaped the mode")
					}
					before, after := decodeFixture(t, raw), decodeFixture(t, out.Bytes())
					sameFixtureShape(t, "", before, after)
				})
			}
		})
	}
}

func decodeFixture(t *testing.T, raw []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out any
	if err := d.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Transformations may replace strings, but never drop keys or change numeric,
// boolean, null or array/object shape. Specific metadata assertions live with
// the family tests; this checks every field of every complete real document.
func sameFixtureShape(t *testing.T, path string, before, after any) {
	t.Helper()
	switch v := before.(type) {
	case map[string]any:
		got, ok := after.(map[string]any)
		if !ok || len(v) != len(got) {
			t.Fatalf("object shape changed at %s", path)
		}
		for key, child := range v {
			value, exists := got[key]
			if !exists {
				t.Fatalf("field removed at %s/%s", path, key)
			}
			sameFixtureShape(t, path+"/"+key, child, value)
		}
	case []any:
		got, ok := after.([]any)
		if !ok || len(v) != len(got) {
			t.Fatalf("array shape changed at %s", path)
		}
		for i, child := range v {
			sameFixtureShape(t, path+"/[]", child, got[i])
		}
	case string:
		if _, ok := after.(string); !ok {
			t.Fatalf("string type changed at %s", path)
		}
	default:
		if !reflect.DeepEqual(v, after) {
			t.Fatalf("scalar/null changed at %s", path)
		}
	}
}
