package features_test

import (
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/features"
)

// TestUnitFeatureCatalogue checks the compiled-in catalogue's rules: names
// sorted and unique, every requirement a gated feature, and the
// installation's own names of the kinds it treats them as.
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
		for _, r := range f.Requires {
			if req, ok := features.Lookup(r); !ok || !req.Gated() {
				t.Errorf("%s requires %s, which is not a gated feature", f.Name, r)
			}
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

// TestUnitSeededTiers checks that each seeded tier holds every smaller
// one's features, that every tier holds operator, and that Enterprise
// holds every gated feature and no limit.
func TestUnitSeededTiers(t *testing.T) {
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
