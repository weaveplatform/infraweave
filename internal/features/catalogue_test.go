package features_test

import (
	"maps"
	"net/url"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/features"
)

// TestUnitFeatureCatalogue checks the compiled-in catalogue's rules: names
// sorted and unique, every requirement a gated feature, every preview
// marker one the feature preview read can show, and the installation's own
// names of the kinds it treats them as.
//
// ref: decision 0021
func TestUnitFeatureCatalogue(t *testing.T) {
	c := features.Catalogue()
	if len(c) == 0 {
		t.Fatal("an empty catalogue")
	}
	c[0].Name = "changed"
	if features.Catalogue()[0].Name == "changed" {
		t.Error("Catalogue returns the catalogue itself")
	}
	c = features.Catalogue()
	for i, f := range c {
		if i > 0 && c[i-1].Name >= f.Name {
			t.Errorf("%s after %s: not sorted and unique", f.Name, c[i-1].Name)
		}
		if got, ok := features.Lookup(f.Name); !ok || got.Name != f.Name {
			t.Errorf("Lookup(%s) = %+v, %t", f.Name, got, ok)
		}
		if f.Gated() != (f.LowestTier != "") {
			t.Errorf("%s: gated %t with lowest tier %q", f.Name, f.Gated(), f.LowestTier)
		}
		if f.Gated() && !slices.Contains(features.SeededTiers, f.LowestTier) {
			t.Errorf("%s has unknown lowest tier %q", f.Name, f.LowestTier)
		}
		for name := range f.Values {
			if !slices.Contains(features.SeededTiers, name) {
				t.Errorf("%s has unknown limit tier %q", f.Name, name)
			}
		}
		for _, r := range f.Requires {
			if req, ok := features.Lookup(r); !ok || !req.Gated() {
				t.Errorf("%s requires %s, which is not a gated feature", f.Name, r)
			}
		}
		if fault := previewFault(f); fault != "" {
			t.Errorf("%s: %s", f.Name, fault)
		}
	}
	marker := func(edit func(*features.Preview)) *features.Preview {
		p := &features.Preview{Name: "X", Description: "Does x.", DocsURL: "https://docs.example.com/x"}
		edit(p)
		return p
	}
	same := func(*features.Preview) {}
	for name, f := range map[string]features.Feature{
		"a preview on a limit":    {Name: "seats", Kind: features.Limit, Preview: marker(same)},
		"a preview on the switch": {Name: features.Billing, Kind: features.Switch, Preview: marker(same)},
		"no name":                 {Name: "x", Kind: features.Capability, Preview: marker(func(p *features.Preview) { p.Name = "" })},
		"no description": {
			Name: "x", Kind: features.Option, Preview: marker(func(p *features.Preview) { p.Description = "" }),
		},
		"no docsUrl": {Name: "x", Kind: features.Capability, Preview: marker(func(p *features.Preview) { p.DocsURL = "" })},
		"an http docsUrl": {
			Name: "x", Kind: features.Capability,
			Preview: marker(func(p *features.Preview) { p.DocsURL = "http://docs.example.com/x" }),
		},
		"an http imageUrl": {
			Name: "x", Kind: features.Option,
			Preview: marker(func(p *features.Preview) { p.ImageURL = "http://docs.example.com/x.png" }),
		},
	} {
		if previewFault(f) == "" {
			t.Errorf("%s: the check accepts %+v", name, *f.Preview)
		}
	}
	for _, f := range []features.Feature{
		{Name: "x", Kind: features.Capability, Preview: marker(same)},
		{Name: "x", Kind: features.Option, Preview: marker(func(p *features.Preview) {
			p.ImageURL = "https://docs.example.com/x.png"
		})},
		{Name: "seats", Kind: features.Limit},
	} {
		if fault := previewFault(f); fault != "" {
			t.Errorf("%s: %s", f.Name, fault)
		}
	}
	if _, ok := features.Lookup("SCANS"); ok {
		t.Error("Lookup found a Spacelift spelling")
	}
	if f, _ := features.Lookup(features.Billing); f.Kind != features.Switch {
		t.Errorf("billing is a %s", f.Kind)
	}
	if f, _ := features.Lookup(
		features.Operator,
	); f.Kind != features.Capability ||
		f.LowestTier != features.Free {
		t.Errorf("operator is a %s from %s", f.Kind, f.LowestTier)
	}
}

// previewFault is what is wrong with f's preview marker, or "" when it has
// none or the feature preview read can show it: only a capability or an
// option is a preview, and its marker has a name, a description, an https
// documentation URL and, when it has one, an https image URL.
func previewFault(f features.Feature) string {
	p := f.Preview
	switch {
	case p == nil:
		return ""
	case !f.Gated():
		return "a preview marker on a " + string(f.Kind)
	case p.Name == "" || p.Description == "" || p.DocsURL == "":
		return "a preview marker without its name, description or docsUrl"
	case !isHTTPS(p.DocsURL) || p.ImageURL != "" && !isHTTPS(p.ImageURL):
		return "a preview marker with a URL that is not https"
	}
	return ""
}

func isHTTPS(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// TestUnitPreviews checks that the preview list holds every marked
// capability and option, with its marker, in catalogue order.
func TestUnitPreviews(t *testing.T) {
	drift := &features.Preview{
		Name: "Drift detection", Description: "Finds resources that changed outside a run.",
		DocsURL: "https://docs.example.com/drift",
	}
	checks := &features.Preview{
		Name: "Aggregated checks", Description: "One `check` per commit.",
		DocsURL: "https://docs.example.com/checks", ImageURL: "https://docs.example.com/checks.png",
	}
	features.WithCatalogue(t, []features.Feature{
		{Name: "aggregated", Kind: features.Option, LowestTier: features.Business, Preview: checks},
		{Name: features.Billing, Kind: features.Switch},
		{Name: "drift", Kind: features.Capability, LowestTier: features.Business, Preview: drift},
		{Name: "stacks", Kind: features.Capability, LowestTier: features.Free},
	})
	got := features.Previews()
	if len(got) != 2 || got[0].Name != "aggregated" || got[1].Name != "drift" {
		t.Fatalf("previews %+v, want aggregated and drift", got)
	}
	if *got[0].Preview != *checks || *got[1].Preview != *drift {
		t.Errorf("markers %+v and %+v", *got[0].Preview, *got[1].Preview)
	}
	if f, _ := features.Lookup("stacks"); f.Preview != nil {
		t.Errorf("stacks is a preview: %+v", *f.Preview)
	}
	features.WithCatalogue(t, []features.Feature{{Name: "stacks", Kind: features.Capability, LowestTier: features.Free}})
	if got := features.Previews(); len(got) != 0 {
		t.Errorf("previews of a catalogue without one: %+v", got)
	}
}

// TestUnitSeededTiers checks that each seeded tier holds every smaller
// one's features, that every tier holds operator, and that Enterprise
// holds every gated feature and no limit.
func TestUnitSeededTiers(t *testing.T) {
	if !slices.Equal(features.SeededTiers, []string{"Free", "Starter", "Business", "Enterprise"}) {
		t.Fatalf("seeded tiers: %v", features.SeededTiers)
	}
	for name, limits := range map[string]map[string]int64{
		"Free":       {"ai-budget": 10, "seats": 2, "private-workers": 0, "public-worker-slots": 1},
		"Starter":    {"seats": 10, "private-workers": 0, "public-worker-slots": 2},
		"Business":   {"private-workers": 3, "public-worker-slots": 2},
		"Enterprise": {},
	} {
		tier, _ := features.Seed(name)
		if !maps.Equal(tier.Limits, limits) {
			t.Errorf("%s limits: %v, want %v", name, tier.Limits, limits)
		}
	}
	for name, lowest := range map[string]string{
		"drift-detection":      "Business",
		"private-worker-pools": "Business",
		"sessions":             "Business",
		"ignored-runs":         "Starter",
		"repos":                "Enterprise",
	} {
		f, _ := features.Lookup(name)
		if f.LowestTier != lowest {
			t.Errorf("%s starts at %s, want %s", name, f.LowestTier, lowest)
		}
	}
	var prev features.Tier
	for _, name := range features.SeededTiers {
		tier, ok := features.Seed(name)
		if !ok || tier.Name != name {
			t.Fatalf("Seed(%s) = %+v, %t", name, tier, ok)
		}
		if !slices.IsSorted(tier.Features) || !slices.Contains(tier.Features, features.Operator) {
			t.Errorf("%s: features %v", name, tier.Features)
		}
		for _, f := range prev.Features {
			if !slices.Contains(tier.Features, f) {
				t.Errorf("%s lacks %s, which %s holds", name, f, prev.Name)
			}
		}
		for l := range tier.Limits {
			if f, _ := features.Lookup(l); f.Kind != features.Limit {
				t.Errorf("%s limits %s, a %s", name, l, f.Kind)
			}
		}
		prev = tier
	}
	enterprise, _ := features.Seed(features.Enterprise)
	if !slices.Equal(enterprise.Features, features.Gated()) || len(enterprise.Limits) != 0 {
		t.Errorf("Enterprise = %+v", enterprise)
	}
	free, _ := features.Seed(features.Free)
	if _, ok := free.Limits["seats"]; !ok {
		t.Errorf("Free has no seats limit: %v", free.Limits)
	}
	if _, ok := features.Seed("Gold"); ok {
		t.Error("Seed found an unseeded tier")
	}
}

// TestUnitClosed checks that a feature whose requirement is missing leaves
// the set.
func TestUnitClosed(t *testing.T) {
	var dependent features.Feature
	for _, f := range features.Catalogue() {
		if len(f.Requires) > 0 {
			dependent = f
			break
		}
	}
	held := append([]string{"stacks", dependent.Name}, dependent.Requires...)
	if got := features.Closed(
		held,
	); !slices.Contains(got, dependent.Name) ||
		!slices.IsSorted(got) {
		t.Errorf("with its requirements: %v", got)
	}
	if got := features.Closed(
		[]string{"stacks", dependent.Name},
	); !slices.Equal(
		got,
		[]string{"stacks"},
	) {
		t.Errorf("without its requirements: %v", got)
	}
	if got := features.Closed(nil); len(got) != 0 {
		t.Errorf("nothing held: %v", got)
	}
}
