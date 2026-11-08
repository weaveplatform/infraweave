package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/devtools/openapibundle"
)

// problems is the register of problem types, the one complete list of
// slugs, each with the status and title its responses carry.
const problems = "../docs/data/problems.json"

// renames is the register of reference names infraweave serves under
// another name.
//
// ref: decision 0001
const renames = "../docs/data/renames.json"

const problemPrefix = "urn:infraweave:problem:"

// TestUnitAPIBundleCurrent fails when a fragment under src/api changes
// without `make bundle`, so the committed and embedded document never drifts
// from what the fragments say.
func TestUnitAPIBundleCurrent(t *testing.T) {
	want, err := openapibundle.Bundle("src/api")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(api.Served, want) {
		t.Error("api/api.openapi.yaml is stale; run make bundle and commit the result")
	}
}

func loadServed(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Served)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestUnitAPIConventionComponents checks that every component decision
// 0003 names exists and says what it is for.
func TestUnitAPIConventionComponents(t *testing.T) {
	c := loadServed(t).Components
	for _, name := range []string{"IdempotencyKey", "Cursor", "Limit"} {
		if p := c.Parameters[name]; p == nil || p.Value.Description == "" {
			t.Errorf("parameter %s missing or undocumented", name)
		}
	}
	for _, name := range []string{
		"RateLimitLimit", "RateLimitRemaining", "RateLimitReset", "RetryAfter", "IdempotencyReplayed",
	} {
		if h := c.Headers[name]; h == nil || h.Value.Description == "" {
			t.Errorf("header %s missing or undocumented", name)
		}
	}
	for _, name := range []string{
		"TooManyRequests", "IdempotencyConflict", "IdempotencyMismatch", "ApiDisabled", "InvalidCursor",
	} {
		if r := c.Responses[name]; r == nil || r.Value.Description == nil ||
			*r.Value.Description == "" {
			t.Errorf("response %s missing or undocumented", name)
		}
	}
	tooMany := c.Responses["TooManyRequests"].Value.Headers
	for _, h := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if tooMany[h] == nil {
			t.Errorf("TooManyRequests lacks %s", h)
		}
	}
	if c.Responses["ApiDisabled"].Value.Headers["Retry-After"] == nil {
		t.Error("ApiDisabled lacks Retry-After")
	}
}

type problem struct {
	ID       string `json:"id"`
	Status   int    `json:"status"`
	Title    string `json:"title"`
	Decision string `json:"decision"`
}

// problemTypes reads the problem register, keyed by slug.
func problemTypes(t *testing.T) map[string]problem {
	t.Helper()
	var reg struct{ Records []problem }
	readRegister(t, problems, &reg)
	listed := map[string]problem{}
	for _, p := range reg.Records {
		listed[p.ID] = p
	}
	if len(listed) == 0 {
		t.Fatalf("%s lists no problem types", problems)
	}
	return listed
}

func readRegister(t *testing.T, file string, v any) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}

// exampleInstance is a problem's instance: the request ID, as
// X-Request-Id accepts it, under urn:infraweave:request:, so a problem
// names the request that the response header and the server log name too.
//
// ref: decision 0022
var exampleInstance = regexp.MustCompile(`^urn:infraweave:request:[A-Za-z0-9._-]{1,128}$`)

// TestUnitAPIProblemTypes checks that every problem example in the spec
// uses a slug the problem register lists, with its status, title and an
// instance naming a request, so the
// register stays the one complete list of problem types. It also checks that
// every slug the register gives to the API conventions (idempotency keys,
// rate limits, a disabled API, invalid cursors) has a response that uses
// it, since the shared response components exist to carry them.
//
// ref: decision 0003
func TestUnitAPIProblemTypes(t *testing.T) {
	listed := problemTypes(t)
	used := map[string]bool{}
	for name, r := range loadServed(t).Components.Responses {
		mt := r.Value.Content.Get("application/problem+json")
		ex, ok := mt.Example.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := ex["type"].(string)
		slug, found := strings.CutPrefix(typ, problemPrefix)
		p, known := listed[slug]
		switch {
		case !found || !known:
			t.Errorf("response %s: type %q is not a slug listed in %s", name, typ, problems)
		case ex["status"] != float64(p.Status):
			t.Errorf("response %s: status %v, %s says %d", name, ex["status"], problems, p.Status)
		case ex["title"] != p.Title:
			t.Errorf("response %s: title %v, %s says %q", name, ex["title"], problems, p.Title)
		case !exampleInstance.MatchString(fmt.Sprint(ex["instance"])):
			t.Errorf(
				"response %s: instance %v is not urn:infraweave:request:<request ID>",
				name,
				ex["instance"],
			)
		}
		used[slug] = true
	}
	for slug, p := range listed {
		if p.Decision == "0003" && !used[slug] {
			t.Errorf("%s gives %s to decision 0003, but no response uses it", problems, slug)
		}
	}
}

// servedName is one name a served document shows its clients. owner is the
// component schema that holds a property or enum value, and is empty for
// every other kind of name.
type servedName struct {
	owner, name, where string
}

// nameWalker collects the names of one served document.
type nameWalker struct {
	names []servedName
}

func (w *nameWalker) add(owner, name, where string) {
	w.names = append(w.names, servedName{owner: owner, name: name, where: where})
}

// schema walks an inline schema. A schema reached through $ref is skipped
// here and walked once under its component name, so that a property is
// attributed to the schema that declares it.
func (w *nameWalker) schema(owner, where string, ref *openapi3.SchemaRef) {
	if ref == nil || ref.Ref != "" || ref.Value == nil {
		return
	}
	s := ref.Value
	for _, v := range s.Enum {
		if value, ok := v.(string); ok {
			w.add(owner, value, where+" enum")
		}
	}
	for p, ps := range s.Properties {
		w.add(owner, p, where+"."+p)
		w.schema(owner, where+"."+p, ps)
	}
	w.schema(owner, where+"[]", s.Items)
	w.schema(owner, where+"{}", s.AdditionalProperties.Schema)
	w.schema(owner, where+" not", s.Not)
	for _, group := range []openapi3.SchemaRefs{s.AllOf, s.AnyOf, s.OneOf} {
		for _, sub := range group {
			w.schema(owner, where, sub)
		}
	}
}

func (w *nameWalker) content(where string, c openapi3.Content) {
	for mt, m := range c {
		w.schema("", where+" "+mt, m.Schema)
	}
}

func (w *nameWalker) parameter(where string, ref *openapi3.ParameterRef) {
	if ref == nil || ref.Ref != "" || ref.Value == nil {
		return
	}
	p := ref.Value
	w.add("", p.Name, where+" "+p.In+" parameter")
	w.schema("", where+" "+p.Name, p.Schema)
	w.content(where+" "+p.Name, p.Content)
}

func (w *nameWalker) header(where, name string, ref *openapi3.HeaderRef) {
	w.add("", name, where+" header")
	if ref == nil || ref.Ref != "" || ref.Value == nil {
		return
	}
	w.schema("", where+" "+name, ref.Value.Schema)
	w.content(where+" "+name, ref.Value.Content)
}

func (w *nameWalker) response(where string, ref *openapi3.ResponseRef) {
	if ref == nil || ref.Ref != "" || ref.Value == nil {
		return
	}
	for name, h := range ref.Value.Headers {
		w.header(where, name, h)
	}
	w.content(where, ref.Value.Content)
}

func (w *nameWalker) requestBody(where string, ref *openapi3.RequestBodyRef) {
	if ref != nil && ref.Ref == "" && ref.Value != nil {
		w.content(where, ref.Value.Content)
	}
}

// servedNames lists every name a client of doc reads or sends: paths, tags,
// operationIds, schema and property names, string enum values, parameter
// names and header names. Descriptions, examples and x- extensions are left
// out, because they hold prose and the mapping back to the reference, not
// names a client uses; spaceliftViolations reads the prose.
func servedNames(doc *openapi3.T) []servedName {
	w := &nameWalker{}
	for _, tag := range doc.Tags {
		w.add("", tag.Name, "tag")
	}
	c := doc.Components
	for name, s := range c.Schemas {
		w.add("", name, "schema "+name)
		w.schema(name, "schema "+name, s)
	}
	for name, p := range c.Parameters {
		w.parameter("parameter "+name, p)
	}
	for name, h := range c.Headers {
		w.header("component", name, h)
	}
	for name, r := range c.Responses {
		w.response("response "+name, r)
	}
	for name, b := range c.RequestBodies {
		w.requestBody("request body "+name, b)
	}
	for path, item := range doc.Paths.Map() {
		w.add("", path, "path")
		for _, p := range item.Parameters {
			w.parameter(path, p)
		}
		for method, op := range item.Operations() {
			where := method + " " + path
			w.add("", op.OperationID, where+" operationId")
			for _, tag := range op.Tags {
				w.add("", tag, where+" tag")
			}
			for _, p := range op.Parameters {
				w.parameter(where, p)
			}
			w.requestBody(where+" request", op.RequestBody)
			for status, r := range op.Responses.Map() {
				w.response(where+" "+status, r)
			}
		}
	}
	return w.names
}

// renamed is one identifier record of the rename register. pattern
// matches the reference name, with * standing for any run of characters;
// owner, when set, limits the match to names that component schema holds.
type renamed struct {
	owner, reference, infraweave string
	pattern                      *regexp.Regexp
}

// renameTable reads the identifier records of the rename register. A record
// with a schema names a property or enum value of that schema only, so a
// common word such as organizationID is refused only where it means what the
// reference meant. Concept records name a reference concept in words and are
// skipped.
func renameTable(t *testing.T) []renamed {
	t.Helper()
	var reg struct {
		Records []struct {
			Kind, Schema, Reference, Infraweave string
		}
	}
	readRegister(t, renames, &reg)
	var rows []renamed
	for _, rec := range reg.Records {
		if rec.Kind != "identifier" {
			continue
		}
		rows = append(rows, renamed{
			owner:      rec.Schema,
			reference:  rec.Reference,
			infraweave: rec.Infraweave,
			pattern: regexp.MustCompile(
				"^" + strings.ReplaceAll(regexp.QuoteMeta(rec.Reference), `\*`, ".*") + "$",
			),
		})
	}
	if len(rows) == 0 {
		t.Fatalf("%s has no identifier records", renames)
	}
	return rows
}

// referenceExtensions map a served element back to the reference's
// GraphQL, Bruno and provider sources, so their values are the reference's
// own names by design. Every other extension, x-enum-descriptions among
// them, is served text and is checked.
var referenceExtensions = map[string]bool{
	"x-bruno":           true,
	"x-graphql":         true,
	"x-graphql-arg":     true,
	"x-graphql-args-of": true,
	"x-graphql-in-path": true,
	"x-graphql-kind":    true,
	"x-graphql-omitted": true,
	"x-graphql-type":    true,
	"x-terraform":       true,
}

// spaceliftViolations lists every key and string of the document that says
// spacelift in any case, outside referenceExtensions, each as its dotted
// location: the location alone for a key, the location and value for a
// string. Reading the YAML rather than the parsed document reaches
// descriptions, summaries and examples that servedNames leaves out.
func spaceliftViolations(t *testing.T, document []byte) []string {
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
				key := n.Content[i].Value
				if referenceExtensions[key] {
					continue
				}
				at := strings.TrimPrefix(where+"."+key, ".")
				if strings.Contains(strings.ToLower(key), "spacelift") {
					found = append(found, at)
				}
				walk(at, n.Content[i+1])
			}
		case yaml.ScalarNode:
			if strings.Contains(strings.ToLower(n.Value), "spacelift") {
				found = append(found, where+": "+n.Value)
			}
		}
	}
	walk("", &root)
	return found
}

func renameViolations(names []servedName, rows []renamed) []string {
	var found []string
	for _, n := range names {
		for _, r := range rows {
			if (r.owner == "" || r.owner == n.owner) && r.pattern.MatchString(n.name) {
				found = append(found, n.where+": "+n.name+" is the reference name "+
					r.reference+", which the rename register renames to "+r.infraweave)
			}
		}
	}
	return found
}

// servedNamesFixture serves one reference name and one reference
// description, so the test proves that a scaffold copied from the reference
// fails both checks rather than passing because a walker never reached its
// names or its prose. The x-terraform value is the reference's own name and
// must pass.
const servedNamesFixture = `openapi: 3.0.3
info: {title: fixture, version: "1"}
paths: {}
components:
  schemas:
    Installation:
      type: object
      description: Spacelift's installation
      properties:
        spaceliftAwsAccountId:
          type: string
          x-terraform: [spacelift_aws_integration.external_id]
`

// TestUnitServedNames fails when the API serves text that says
// spacelift in any case, or a reference name that the rename register
// replaces. Families are scaffolded from Spacelift's spec, so each
// arrives carrying Spacelift's names and descriptions; a scaffold that keeps
// one would make infraweave's clients, SDK and provider depend on a vendor's
// name, and its generated docs describe infraweave as Spacelift. The
// spacelift check catches a rename the table lacks and a description left
// unrewritten; the table check catches reference names that never said
// spacelift, such as the non-root admin settings.
func TestUnitServedNames(t *testing.T) {
	rows := renameTable(t)
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.infraweave), "spacelift") {
			t.Errorf(
				"%s renames %s to %s, which contains spacelift",
				renames,
				r.reference,
				r.infraweave,
			)
		}
	}
	t.Run("served", func(t *testing.T) {
		names := servedNames(loadServed(t))
		if len(names) == 0 {
			t.Fatal("the API serves no names")
		}
		for _, v := range append(spaceliftViolations(t, api.Served), renameViolations(names, rows)...) {
			t.Error(v)
		}
	})
	t.Run("fixture", func(t *testing.T) {
		doc, err := openapi3.NewLoader().LoadFromData([]byte(servedNamesFixture))
		if err != nil {
			t.Fatal(err)
		}
		names := servedNames(doc)
		want := []string{
			"components.schemas.Installation.description: Spacelift's installation",
			"components.schemas.Installation.properties.spaceliftAwsAccountId",
		}
		if got := spaceliftViolations(t, []byte(servedNamesFixture)); !slices.Equal(got, want) {
			t.Errorf("spacelift check found %q, want %q", got, want)
		}
		if len(renameViolations(names, rows)) != 1 {
			t.Errorf(
				"rename check found %q, want spaceliftAwsAccountId",
				renameViolations(names, rows),
			)
		}
	})
}
