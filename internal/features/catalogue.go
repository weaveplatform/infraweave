package features

import (
	"maps"
	"slices"
)

// Kind is a catalogue entry's kind.
//
// ref: decision 0021
type Kind string

// The kinds of catalogue entry.
const (
	// Capability gates a set of operations.
	Capability Kind = "capability"
	// Option gates one value of a field inside an operation.
	Option Kind = "option"
	// Limit counts a thing an organization creates.
	Limit Kind = "limit"
	// Switch is billing, which turns entitlement enforcement on.
	Switch Kind = "switch"
)

// The seeded tiers, smallest first.
const (
	Free        = "Free"
	Starter     = "Starter"
	StarterPlus = "Starter+"
	Business    = "Business"
	Enterprise  = "Enterprise"
)

// SeededTiers are the tiers infraweave installs once, smallest first.
var SeededTiers = []string{Free, Starter, StarterPlus, Business, Enterprise}

// The names the installation holds rather than a tier: Billing, the
// switch, and Operator, the operator API's capability. features.disabled
// rejects both, since switching either off would leave the operator no
// way to manage the installation.
const (
	Billing  = "billing"
	Operator = "operator"
)

// Feature is one entry of the catalogue.
type Feature struct {
	Name string
	Kind Kind
	// LowestTier is the smallest seeded tier that holds a capability or an
	// option; a limit or the switch has none.
	LowestTier string
	// Requires are the features an entitlement must hold for this one to
	// be in it.
	Requires []string
	// Values are a limit's seeded values by tier name; a tier without one
	// has no limit.
	Values map[string]int64
	// Phase is the plan phase that owns it.
	Phase string
	// TierFeatures are the BillingTierFeature values it covers, which
	// tierFeatures reports.
	TierFeatures []string
}

// Gated reports whether the feature is one an entitlement holds or lacks:
// a capability or an option.
func (f Feature) Gated() bool { return f.Kind == Capability || f.Kind == Option }

// Catalogue is every feature, in name order.
func Catalogue() []Feature { return slices.Clone(catalogue) }

// Lookup returns the feature named name.
func Lookup(name string) (Feature, bool) {
	i, ok := slices.BinarySearchFunc(catalogue, name, func(f Feature, n string) int {
		switch {
		case f.Name < n:
			return -1
		case f.Name > n:
			return 1
		}
		return 0
	})
	if !ok {
		return Feature{}, false
	}
	return catalogue[i], true
}

// Tier is a tier's content: its features, sorted, and its limit values by
// limit name. A limit absent from Limits has no limit.
type Tier struct {
	Name     string
	Features []string
	Limits   map[string]int64
}

// Seed is the content of the seeded tier name: every gated feature whose
// lowest seeded tier is name or smaller, and each limit's value for name.
// Enterprise holds every gated feature and no limit.
func Seed(name string) (Tier, bool) {
	rank := slices.Index(SeededTiers, name)
	if rank < 0 {
		return Tier{}, false
	}
	t := Tier{Name: name, Features: []string{}, Limits: map[string]int64{}}
	for _, f := range catalogue {
		switch {
		case f.Gated() && slices.Index(SeededTiers, f.LowestTier) <= rank:
			t.Features = append(t.Features, f.Name)
		case f.Kind == Limit && name != Enterprise:
			if v, ok := f.Values[name]; ok {
				t.Limits[f.Name] = v
			}
		}
	}
	return t, true
}

// Gated is every gated feature's name, sorted: what Enterprise holds.
func Gated() []string {
	var out []string
	for _, f := range catalogue {
		if f.Gated() {
			out = append(out, f.Name)
		}
	}
	return out
}

// Closed returns held less every feature whose requirements are not all in
// what remains, repeated until nothing more drops, so a requirement's
// requirement counts too.
func Closed(held []string) []string {
	in := map[string]bool{}
	for _, h := range held {
		in[h] = true
	}
	for changed := true; changed; {
		changed = false
		for name := range in {
			f, _ := Lookup(name)
			for _, r := range f.Requires {
				if !in[r] {
					delete(in, name)
					changed = true
					break
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(in))
}
