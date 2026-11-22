package dataview

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	catalogue "github.com/weaveplatform/infraweave/internal/features"
)

func TestUnitRender(t *testing.T) {
	want := documentFixture(t, "tiers.golden")
	features := documentFixture(t, "features.json")
	capabilities := documentFixture(t, "capabilities.json")
	got, err := Render([]byte(features), []byte(capabilities))
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(want, string(got)); d != "" {
		t.Errorf("view (-want +got):\n%s", d)
	}
}

func TestUnitRenderErrors(t *testing.T) {
	features := documentFixture(t, "features.json")
	capabilities := documentFixture(t, "capabilities.json")
	for name, tc := range map[string]struct {
		features, capabilities string
		want                   error
		text                   string
	}{
		"features not JSON":     {features: "{", capabilities: capabilities, text: FeaturesFile},
		"capabilities not JSON": {features: features, capabilities: "[", text: CapabilitiesFile},
		"unknown tier": {
			features:     `{"records": [{"id": "x", "kind": "option", "lowestTier": "Gold"}]}`,
			capabilities: capabilities,
			want:         ErrUnknownTier,
		},
		"unknown limit tier": {
			features:     `{"records":[{"id":"seats","kind":"limit","values":{"gold":2}}]}`,
			capabilities: capabilities,
			want:         ErrUnknownTier,
		},
		"no phase": {
			features:     features,
			capabilities: `{"records": [{"id": "tag:X", "kind": "tag", "infraweave": {}}]}`,
			want:         ErrNoPhase,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Render([]byte(tc.features), []byte(tc.capabilities))
			switch {
			case err == nil:
				t.Fatal("no error")
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("err = %v, want %v", err, tc.want)
			case !strings.Contains(err.Error(), tc.text):
				t.Errorf("err = %v, want it to name %q", err, tc.text)
			}
		})
	}
}

func TestUnitPhaseOrder(t *testing.T) {
	var cases []struct {
		A    string `json:"a"`
		B    string `json:"b"`
		Want int    `json:"want"`
	}
	if err := json.Unmarshal([]byte(documentFixture(t, "ordering.json")), &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatalf("ordering cases = %d, want 4", len(cases))
	}
	for _, tc := range cases {
		if got := phaseOrder(tc.A, tc.B); got != tc.Want {
			t.Errorf("phaseOrder(%s, %s) = %d, want %d", tc.A, tc.B, got, tc.Want)
		}
	}
}

func TestUnitWrite(t *testing.T) {
	want := documentFixture(t, "tiers.golden")
	features := documentFixture(t, "features.json")
	capabilities := documentFixture(t, "capabilities.json")
	dir := t.TempDir()
	out := filepath.Join(dir, "tiers.md")
	if err := Write(dir, out); err == nil {
		t.Error("no error without features.json")
	}
	mustWrite(t, filepath.Join(dir, FeaturesFile), features)
	if err := Write(dir, out); err == nil {
		t.Error("no error without capabilities.json")
	}
	mustWrite(t, filepath.Join(dir, CapabilitiesFile), "[")
	if err := Write(dir, out); err == nil {
		t.Error("no error with capabilities.json not JSON")
	}
	mustWrite(t, filepath.Join(dir, CapabilitiesFile), capabilities)
	if err := Write(dir, filepath.Join(dir, "missing", "tiers.md")); err == nil {
		t.Error("no error writing into a missing directory")
	}
	if err := Write(dir, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("wrote %q", b)
	}
}

// TestUnitCommittedView fails when the committed view is not what the
// committed registers render, so go test catches a register edit made
// without make generate.
func TestUnitCommittedView(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	f, err := os.ReadFile(filepath.Join(root, "docs", "data", FeaturesFile))
	if err != nil {
		t.Fatal(err)
	}
	c, err := os.ReadFile(filepath.Join(root, "docs", "data", CapabilitiesFile))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Render(f, c)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join(root, "docs", "implementation", "tiers.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may carry CRLF; the view is written with LF.
	text := strings.ReplaceAll(string(committed), "\r\n", "\n")
	if d := cmp.Diff(text, string(got)); d != "" {
		t.Errorf(
			"docs/implementation/tiers.md is stale; run make generate (-committed +rendered):\n%s",
			d,
		)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestUnitTierNames keeps the generated view aligned with the server's seeds.
func TestUnitTierNames(t *testing.T) {
	names := make([]string, len(tiers))
	for i, tier := range tiers {
		names[i] = tier.name
	}
	if !slices.Equal(names, catalogue.SeededTiers) {
		t.Errorf("view tiers %v, server tiers %v", names, catalogue.SeededTiers)
	}
}
