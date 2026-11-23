package dataview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The catalogue's errors: a requirement, or a tier-feature record's
// covering feature, that names no feature of features.json, which the
// generated catalogue would carry as a dangling name; and a preview marker
// the feature preview read could not show, which features.schema.json
// refuses too.
var (
	ErrUnknownRequirement = errors.New("dataview: a feature requires a name the catalogue lacks")
	ErrUnknownTierFeature = errors.New(
		"dataview: a tier-feature record names a feature the catalogue lacks",
	)
	ErrInvalidPreview = errors.New("dataview: an invalid preview marker")
)

// catalogueFeature is a features.json record as the generated catalogue
// needs it.
type catalogueFeature struct {
	ID         string           `json:"id"`
	Kind       string           `json:"kind"`
	LowestTier string           `json:"lowestTier"`
	Values     map[string]int64 `json:"values"`
	Requires   []string         `json:"requires"`
	Preview    *preview         `json:"preview"`
}

// preview is a record's preview marker.
type preview struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	DocsURL     string `json:"docsUrl"`
	ImageURL    string `json:"imageUrl"`
}

// check refuses a marker on a limit or the switch, a marker without its
// name, description or documentation URL, and a URL that is not https.
func (p *preview) check(id, kind string) error {
	switch {
	case kind != "capability" && kind != "option":
		return fmt.Errorf("%w: %s is a %s", ErrInvalidPreview, id, kind)
	case p.Name == "" || p.Description == "" || p.DocsURL == "":
		return fmt.Errorf("%w: %s lacks its name, description or docsUrl", ErrInvalidPreview, id)
	case !httpsURL(p.DocsURL) || p.ImageURL != "" && !httpsURL(p.ImageURL):
		return fmt.Errorf("%w: %s has a URL that is not https", ErrInvalidPreview, id)
	}
	return nil
}

// httpsURL reports whether s is an absolute https URL.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// tierFeature is a tier-feature record of capabilities.json: a
// BillingTierFeature value and the infraweave features that cover it.
type tierFeature struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Infraweave struct {
		Feature string `json:"feature"`
	} `json:"infraweave"`
}

// codeSpan is a markdown code span, which is how a register names a feature.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// covering names the catalogue features a tier-feature record's infraweave
// feature text names: its code spans before any ": ", which starts the
// record's commentary (`audit-trail`: superseded … by `AUDITING`).
func covering(text string) []string {
	head, _, _ := strings.Cut(text, ": ")
	spans := codeSpan.FindAllStringSubmatch(head, -1)
	out := make([]string, 0, len(spans))
	for _, m := range spans {
		out = append(out, m[1])
	}
	return out
}

// WriteCatalogue writes the Go catalogue of the registers in dataDir to out.
func WriteCatalogue(dataDir, out string) error {
	f, err := os.ReadFile(filepath.Join(dataDir, FeaturesFile))
	if err != nil {
		return fmt.Errorf("dataview: %w", err)
	}
	c, err := os.ReadFile(filepath.Join(dataDir, CapabilitiesFile))
	if err != nil {
		return fmt.Errorf("dataview: %w", err)
	}
	b, err := Catalogue(f, c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, b, 0o600); err != nil {
		return fmt.Errorf("dataview: %w", err)
	}
	return nil
}

// Catalogue returns internal/features/catalogue.gen.go for the two
// registers: every feature in name order, with its kind, lowest seeded
// tier, requirements, seeded limit values by tier name, the
// BillingTierFeature values the tier-feature records say it covers, and
// its preview marker.
func Catalogue(featuresJSON, capabilitiesJSON []byte) ([]byte, error) {
	var fs struct {
		Records []catalogueFeature `json:"records"`
	}
	if err := json.Unmarshal(featuresJSON, &fs); err != nil {
		return nil, fmt.Errorf("dataview: %s: %w", FeaturesFile, err)
	}
	var cs struct {
		Records []tierFeature `json:"records"`
	}
	if err := json.Unmarshal(capabilitiesJSON, &cs); err != nil {
		return nil, fmt.Errorf("dataview: %s: %w", CapabilitiesFile, err)
	}
	names := map[string]bool{}
	for _, f := range fs.Records {
		names[f.ID] = true
	}
	covers := map[string][]string{}
	for _, r := range cs.Records {
		if r.Kind != "tier-feature" {
			continue
		}
		value := strings.TrimPrefix(r.ID, "tier-feature:")
		for _, name := range covering(r.Infraweave.Feature) {
			if !names[name] {
				return nil, fmt.Errorf("%w: %s names %q", ErrUnknownTierFeature, r.ID, name)
			}
			covers[name] = append(covers[name], value)
		}
	}
	all := slices.SortedFunc(slices.Values(fs.Records), func(a, b catalogueFeature) int {
		return strings.Compare(a.ID, b.ID)
	})
	var b bytes.Buffer
	b.WriteString("// Code generated by tools/dataview from docs/data/" + FeaturesFile +
		" and " + CapabilitiesFile + ". DO NOT EDIT.\n\n")
	b.WriteString("package features\n\n")
	b.WriteString("var catalogue = []Feature{\n")
	for _, f := range all {
		if f.LowestTier != "" && tierIndex(f.LowestTier) < 0 {
			return nil, fmt.Errorf("%w: %s has %q", ErrUnknownTier, f.ID, f.LowestTier)
		}
		for _, r := range f.Requires {
			if !names[r] {
				return nil, fmt.Errorf("%w: %s requires %q", ErrUnknownRequirement, f.ID, r)
			}
		}
		fmt.Fprintf(&b, "\t{Name: %q, Kind: %q", f.ID, f.Kind)
		if f.LowestTier != "" {
			fmt.Fprintf(&b, ", LowestTier: %q", f.LowestTier)
		}
		if len(f.Requires) > 0 {
			fmt.Fprintf(&b, ", Requires: %s", goStrings(f.Requires))
		}
		if err := checkLimitTiers(f.ID, f.Values); err != nil {
			return nil, err
		}
		if len(f.Values) > 0 {
			b.WriteString(", Values: map[string]int64{")
			for _, t := range tiers {
				if v, ok := f.Values[t.key]; ok {
					fmt.Fprintf(&b, "%q: %d, ", t.name, v)
				}
			}
			b.WriteString("}")
		}
		if v := covers[f.ID]; len(v) > 0 {
			slices.Sort(v)
			fmt.Fprintf(&b, ", TierFeatures: %s", goStrings(slices.Compact(v)))
		}
		if p := f.Preview; p != nil {
			if err := p.check(f.ID, f.Kind); err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, ", Preview: &Preview{Name: %q, Description: %q, DocsURL: %q", p.Name, p.Description, p.DocsURL)
			if p.ImageURL != "" {
				fmt.Fprintf(&b, ", ImageURL: %q", p.ImageURL)
			}
			b.WriteString("}")
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("dataview: format the catalogue: %w", err)
	}
	return out, nil
}

// goStrings is ss as a Go []string literal.
func goStrings(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}

// checkLimitTiers refuses a limit key the seeded view would otherwise drop.
func checkLimitTiers[V int | int64](name string, values map[string]V) error {
	for key := range values {
		if !slices.ContainsFunc(tiers, func(t tier) bool { return t.key == key }) {
			return fmt.Errorf("%w: %s has limit key %q", ErrUnknownTier, name, key)
		}
	}
	return nil
}
