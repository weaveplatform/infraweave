package api_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/api"
)

// projectReference matches what only a reader of the repository can
// follow: a decision record, a divergence or open question ID, a research
// report, or the docs tree.
var projectReference = regexp.MustCompile(
	`(?i)\bdecisions? \d{4}\b|\b(DV|PD|OQ)-\d+\b|\breports? \d{1,2}\b|docs/(research|implementation)`,
)

// proseKeys are the keys whose values a client's generated documentation
// shows.
var proseKeys = map[string]bool{"description": true, "summary": true, "title": true}

// projectReferences is every prose value of document that cites the
// project's own records, by its location. Extensions, such as
// x-infraweave-divergence's IDs, are machine-read and left out.
func projectReferences(t *testing.T, document []byte) []string {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal(document, &root); err != nil {
		t.Fatal(err)
	}
	var found []string
	var walk func(where string, n *yaml.Node)
	walk = func(where string, n *yaml.Node) {
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				walk(where, c)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value := n.Content[i].Value, n.Content[i+1]
				at := where + "." + key
				if value.Kind == yaml.ScalarNode && proseKeys[key] &&
					projectReference.MatchString(value.Value) {
					found = append(found, at+": "+value.Value)
				}
				walk(at, value)
			}
		}
	}
	walk("", &root)
	return found
}

// proseFixture cites a decision in a description and a divergence ID in
// an extension, which must pass.
const proseFixture = `openapi: 3.0.3
info: {title: fixture, version: "1"}
paths: {}
components:
  schemas:
    Tier:
      type: object
      description: A tier (decision 0021, DV-079).
      x-infraweave-divergence: [DV-079]
`

// TestUnitServedProse fails when a served document's prose cites the
// project's decisions, divergences or reports: the specs are the APIs'
// public documentation, whose readers cannot follow a reference into the
// repository's design records.
func TestUnitServedProse(t *testing.T) {
	operator, err := os.ReadFile("operator.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range append(projectReferences(t, api.Served), projectReferences(t, operator)...) {
		t.Error(v)
	}
	want := []string{".components.schemas.Tier.description: A tier (decision 0021, DV-079)."}
	if got := projectReferences(t, []byte(proseFixture)); !slices.Equal(got, want) {
		t.Errorf("fixture: %q, want %q", got, want)
	}
}
