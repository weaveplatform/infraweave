package sanitize_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/sanitize"
)

// This document is emitted by the recorded public experimental build, not by
// an admitted stable worker. Sanitizer compatibility does not admit its CLI.
func TestUnitActualDeferredResourcePlan(t *testing.T) {
	raw, err := os.ReadFile("../../../test/fixtures/plan-payloads/terraform-deferred/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		t.Run(string(mode), func(t *testing.T) {
			var out bytes.Buffer
			if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
				t.Fatal(err)
			}
			if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
				t.Fatal("NONE changed the tool-produced deferred plan")
			}
			var original, got map[string]any
			if json.Unmarshal(raw, &original) != nil || json.Unmarshal(out.Bytes(), &got) != nil {
				t.Fatal("invalid tool JSON")
			}
			before := original["deferred_changes"].([]any)[0].(map[string]any)
			after := got["deferred_changes"].([]any)[0].(map[string]any)
			br := before["resource_change"].(map[string]any)
			ar := after["resource_change"].(map[string]any)
			bc := br["change"].(map[string]any)
			ac := ar["change"].(map[string]any)
			values := ac["after"].(map[string]any)["input"].(map[string]any)
			secret, public := "dummy-action-secret-675", "visible-675"
			if mode != sanitize.None {
				secret = sanitize.Value(secret).(string)
			}
			if mode == sanitize.Default {
				public = sanitize.Value(public).(string)
			}
			if values["private"] != secret || values["copy"] != secret || values["public"] != public {
				t.Fatal("deferred sensitive values or copies escaped the mode")
			}
			delete(bc, "after")
			delete(ac, "after")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("deferred identity, unknown index, reason, masks or absent/null values changed")
			}
		})
	}
}

func deferredFixture(t *testing.T) (map[string]any, map[string]any, map[string]any, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("../../../test/fixtures/plan-payloads/terraform-deferred/plan.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	d := doc["deferred_changes"].([]any)[0].(map[string]any)
	r := d["resource_change"].(map[string]any)
	return doc, d, r, r["change"].(map[string]any)
}

// Authored corruptions exercise the fail-closed boundary, separately from the
// unchanged tool-produced fixture used above.
func TestUnitDeferredResourceRefusals(t *testing.T) {
	const secret = "dummy-action-secret-675"
	cases := map[string]func(map[string]any, map[string]any, map[string]any){
		"future envelope value": func(d, r, c map[string]any) { d[secret] = secret },
		"reason type":           func(d, r, c map[string]any) { d["reason"] = false },
		"resource missing":      func(d, r, c map[string]any) { delete(d, "resource_change") },
		"resource type":         func(d, r, c map[string]any) { d["resource_change"] = secret },
		"future resource value": func(d, r, c map[string]any) { r[secret] = secret },
		"identity type":         func(d, r, c map[string]any) { r["address"] = false },
		"unknown index type":    func(d, r, c map[string]any) { r["index_unknown"] = secret },
		"index value tree":      func(d, r, c map[string]any) { r["index"] = map[string]any{"token": secret} },
		"fractional index":      func(d, r, c map[string]any) { r["index"] = 1.25 },
		"change missing":        func(d, r, c map[string]any) { delete(r, "change") },
		"change type":           func(d, r, c map[string]any) { r["change"] = secret },
		"future change value":   func(d, r, c map[string]any) { c[secret] = secret },
		"unknown mask value":    func(d, r, c map[string]any) { c["after_unknown"] = secret },
		"actions type":          func(d, r, c map[string]any) { c["actions"] = secret },
		"action value tree":     func(d, r, c map[string]any) { c["actions"] = []any{map[string]any{"token": secret}} },
		"paths type":            func(d, r, c map[string]any) { c["replace_paths"] = secret },
		"path type":             func(d, r, c map[string]any) { c["replace_paths"] = []any{secret} },
		"path value tree":       func(d, r, c map[string]any) { c["replace_paths"] = []any{[]any{map[string]any{"token": secret}}} },
		"path fractional index": func(d, r, c map[string]any) { c["replace_paths"] = []any{[]any{1.25}} },
		"import type":           func(d, r, c map[string]any) { c["importing"] = secret },
		"future import value":   func(d, r, c map[string]any) { c["importing"] = map[string]any{secret: secret} },
		"import unknown type":   func(d, r, c map[string]any) { c["importing"] = map[string]any{"unknown": secret} },
		"import id type":        func(d, r, c map[string]any) { c["importing"] = map[string]any{"id": false} },
		"import identity type":  func(d, r, c map[string]any) { c["importing"] = map[string]any{"identity": secret} },
		"before identity type":  func(d, r, c map[string]any) { c["before_identity"] = secret },
		"after identity type":   func(d, r, c map[string]any) { c["after_identity"] = secret },
		"generated config tree": func(d, r, c map[string]any) { c["generated_config"] = map[string]any{"token": secret} },
	}
	for name, mutate := range cases {
		for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				doc, d, r, c := deferredFixture(t)
				mutate(d, r, c)
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				err = sanitize.Plan(&out, bytes.NewReader(raw), mode)
				if !errors.Is(err, sanitize.ErrMalformed) && !errors.Is(err, sanitize.ErrUnsupported) {
					t.Fatalf("malformed deferred values accepted: %v", err)
				}
				if out.Len() != 0 || strings.Contains(err.Error(), secret) {
					t.Fatal("refusal wrote or echoed raw values")
				}
			})
		}
	}
}

func TestUnitDeferredResourceMetadata(t *testing.T) {
	for _, mode := range []sanitize.Mode{sanitize.Default, sanitize.Smart, sanitize.None} {
		t.Run(string(mode), func(t *testing.T) {
			doc, d, r, c := deferredFixture(t)
			d["reason"] = "deferred_prereq"
			r["index"] = 0
			r["index_unknown"] = false
			r["address"] = "terraform_data.deferred[0]"
			c["replace_paths"] = []any{[]any{"input", 0, "private"}}
			c["before_identity"] = nil
			c["after_identity"] = map[string]any{"token": "dummy-action-secret-675"}
			c["importing"] = map[string]any{"id": "dummy-action-secret-675", "unknown": false, "identity": map[string]any{"token": "dummy-action-secret-675"}}
			c["generated_config"] = "generated dummy-action-secret-675"
			raw, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := sanitize.Plan(&out, bytes.NewReader(raw), mode); err != nil {
				t.Fatal(err)
			}
			if mode == sanitize.None && !bytes.Equal(raw, out.Bytes()) {
				t.Fatal("NONE changed authored metadata")
			}
			var got map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			gr := got["deferred_changes"].([]any)[0].(map[string]any)["resource_change"].(map[string]any)
			gc := gr["change"].(map[string]any)
			secret, generated := "dummy-action-secret-675", "generated dummy-action-secret-675"
			if mode != sanitize.None {
				secret = sanitize.Value(secret).(string)
				generated = sanitize.Value(generated).(string)
			}
			if gc["after_identity"].(map[string]any)["token"] != secret || gc["importing"].(map[string]any)["id"] != secret || gc["generated_config"] != generated {
				t.Fatal("deferred identity/import/generated values escaped mode")
			}
			if gr["index"] != float64(0) || gr["index_unknown"] != false || gc["before_identity"] != nil ||
				!reflect.DeepEqual(gc["replace_paths"], []any{[]any{"input", float64(0), "private"}}) {
				t.Fatal("structural metadata or null changed")
			}
		})
	}
}
