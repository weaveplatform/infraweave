package sanitize_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

const developmentActions = "../../../test/fixtures/plan-payloads/terraform-deferred-actions-development/"

func TestUnitActualDevelopmentDeferredActions(t *testing.T) {
	for _, name := range []string{"plan.json", "count-unknown/plan.json"} {
		raw, err := os.ReadFile(developmentActions + name)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				var out bytes.Buffer
				if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
					t.Fatal(err)
				}
				before, after := decodeFixture(t, raw).(map[string]any), decodeFixture(t, out.Bytes()).(map[string]any)
				if !reflect.DeepEqual(before["deferred_action_invocations"], after["deferred_action_invocations"]) {
					t.Fatal("actual metadata-only invocation changed")
				}
				if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
					t.Fatal("NONE changed captured bytes")
				}
				secret := []byte("dummy-action-secret-675")
				if bytes.Contains(out.Bytes(), secret) != (mode == sanitize.None && bytes.Contains(raw, secret)) {
					t.Fatal("experimental plan values escaped the mode")
				}
				sameFixtureShape(t, "", before, after)
			})
		}
	}
}

// Authored composition of two real shapes exercises masks if a producer adds
// action values inside the wrapper. It is separate from the unchanged captures.
func TestUnitDeferredActionValueMasksAndSelectedUnits(t *testing.T) {
	_, invocation := actionDocument(t, actionFixture(t))
	inner := map[string]any{"format_version": "1.2", "deferred_action_invocations": []any{map[string]any{"reason": "deferred_prereq", "action_invocation": invocation}}}
	raw, err := json.Marshal(
		map[string]any{
			"units": []any{
				map[string]any{"path": ".", "plan": inner},
				map[string]any{
					"path": "child",
					"plan": map[string]any{
						"format_version":   "1.2",
						"resource_changes": []any{map[string]any{"change": map[string]any{"after": map[string]any{"copy": "dummy-action-secret-675", "public": "visible-675"}}}},
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		var out bytes.Buffer
		if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte("dummy-action-secret-675")) != (mode == sanitize.None) {
			t.Fatal("action sensitivity did not propagate into selected unit copies")
		}
		if bytes.Contains(out.Bytes(), []byte("visible-675")) != (mode != sanitize.Default) {
			t.Fatal("public value mode differs")
		}
		if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
			t.Fatal("NONE changed wrapper bytes")
		}
	}
}

func TestUnitDeferredActionWrapperRefusals(t *testing.T) {
	const secret = "dummy-invalid-deferred-675"
	for _, wrapper := range []string{
		`{"reason":"deferred_prereq","action_invocation":{},"` + secret + `":"` + secret + `"}`,
		`{"reason":false,"action_invocation":{}}`,
		`{"action_invocation":{}}`,
		`{"reason":"deferred_prereq"}`,
		`{"reason":"deferred_prereq","action_invocation":null}`,
		`{"reason":"deferred_prereq","action_invocation":"` + secret + `"}`,
		`{"reason":"deferred_prereq","action_invocation":{"config_values":{"token":"` + secret + `"},"config_sensitive":"` + secret + `"}}`,
		`{"reason":"deferred_prereq","action_invocation":{"` + secret + `":"` + secret + `"}}`,
	} {
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			var out bytes.Buffer
			err := sanitize.Plan(&out, strings.NewReader(`{"format_version":"1.2","deferred_action_invocations":[`+wrapper+`]}`), mode)
			if (!errors.Is(err, sanitize.ErrMalformed) && !errors.Is(err, sanitize.ErrUnsupported)) || out.Len() != 0 || strings.Contains(err.Error(), secret) {
				t.Fatalf("wrapper was accepted or leaked: %v", err)
			}
		}
	}
}

func TestUnitDevelopmentProducerProvenance(t *testing.T) {
	data, err := os.ReadFile(developmentActions + "provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Source            string            `json:"source_commit"` //nolint:tagliatelle // source capture spelling.
		PatchedSerializer bool              `json:"jsonSerializerPatched"`
		Class             string            `json:"classification"`
		Files             map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Source != "ae094fc0ee6d4f3d2a990b40ac48a6d2dbaeb53e" || record.PatchedSerializer || !strings.Contains(record.Class, "NOT official release evidence") ||
		len(record.Files) != 7 {
		t.Fatal("experimental provenance boundary changed")
	}
	for name, want := range record.Files {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(developmentActions + name)
			if err != nil {
				t.Fatal(err)
			}
			got := sha256.Sum256(raw)
			if hex.EncodeToString(got[:]) != want {
				t.Fatal("captured source, patch or output changed without provenance")
			}
		})
	}
}
