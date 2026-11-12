package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
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

// servedDocuments loads every document the server serves, by file: the
// API's and the operator and worker APIs', so a check of the controls
// every surface shares runs on each.
//
// ref: decision 0003 § "API surfaces"
func servedDocuments(t *testing.T) map[string]*openapi3.T {
	t.Helper()
	docs := map[string]*openapi3.T{"api.openapi.yaml": loadServed(t)}
	for _, file := range []string{"operator.openapi.yaml", "worker.openapi.yaml"} {
		doc, err := openapi3.NewLoader().LoadFromFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := doc.Validate(context.Background()); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		docs[file] = doc
	}
	return docs
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
	for file, doc := range servedDocuments(t) {
		checkProblemExamples(t, file, doc, listed, used)
	}
	for slug, p := range listed {
		if p.Decision == "0003" && !used[slug] {
			t.Errorf("%s gives %s to decision 0003, but no response uses it", problems, slug)
		}
	}
}

// checkProblemExamples checks each problem example of doc's responses
// against the register listed, and records each slug it uses in used.
func checkProblemExamples(
	t *testing.T,
	file string,
	doc *openapi3.T,
	listed map[string]problem,
	used map[string]bool,
) {
	t.Helper()
	for name, r := range doc.Components.Responses {
		name = file + " " + name
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

// TestUnitAPIStrictInputs fails when a request body reaches an object
// schema that does not set additionalProperties: false, so a misspelt field
// is refused instead of ignored. The MergePatch body of a PATCH beside an
// update PUT names no members; the route table checks it against its PUT's
// input.
//
// ref: decision 0022
func TestUnitAPIStrictInputs(t *testing.T) {
	docs := servedDocuments(t)
	if docs["api.openapi.yaml"].Components.Schemas["MergePatch"] == nil {
		t.Fatal("no MergePatch schema")
	}
	for file, doc := range docs {
		checkStrictInputs(t, file, doc)
	}
}

// checkStrictInputs checks every object schema doc's request bodies reach.
func checkStrictInputs(t *testing.T, file string, doc *openapi3.T) {
	t.Helper()
	var merge *openapi3.Schema
	if m := doc.Components.Schemas["MergePatch"]; m != nil {
		merge = m.Value
	}
	seen := map[*openapi3.Schema]bool{}
	var walk func(at string, r *openapi3.SchemaRef)
	walk = func(at string, r *openapi3.SchemaRef) {
		if r == nil || r.Value == nil || seen[r.Value] || r.Value == merge {
			return
		}
		s := r.Value
		seen[s] = true
		if len(s.Properties) > 0 {
			if has := s.AdditionalProperties.Has; has == nil || *has {
				t.Errorf("%s (%s) allows members it does not name", at, r.Ref)
			}
			for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
				walk(at+"/"+name, s.Properties[name])
			}
		}
		for _, p := range s.AllOf {
			if len(p.Value.Properties) > 0 {
				t.Errorf("%s composes members with allOf, which a strict schema cannot", at)
			}
		}
		walk(at+"/items", s.Items)
		for _, e := range append(slices.Clone(s.OneOf), s.AnyOf...) {
			walk(at, e)
		}
	}
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op.RequestBody == nil || op.RequestBody.Value == nil {
				continue
			}
			for typ, mt := range op.RequestBody.Value.Content {
				walk(file+" "+method+" "+path+" "+typ, mt.Schema)
			}
		}
	}
}

// TestUnitAPIUpdateRoutes fails unless every update PUT has a PATCH
// beside it taking a merge patch, both recording merge-patch, and its item
// is versioned: ETag on the 200 of its GET, PUT and PATCH, and If-Match
// with a 412 on its PUT, PATCH and DELETE, each recording if-match. The
// PATCH, like every PATCH, takes an Idempotency-Key.
//
// ref: decision 0022
func TestUnitAPIUpdateRoutes(t *testing.T) {
	doc := loadServed(t)
	divergence := func(op *openapi3.Operation) []string {
		raw, _ := op.Extensions["x-infraweave-divergence"].([]any)
		out := make([]string, 0, len(raw))
		for _, d := range raw {
			out = append(out, fmt.Sprint(d))
		}
		return out
	}
	takes := func(op *openapi3.Operation, name string) bool {
		return slices.ContainsFunc(op.Parameters, func(p *openapi3.ParameterRef) bool {
			return p.Value != nil && p.Value.In == "header" && p.Value.Name == name
		})
	}
	updates := 0
	for path, item := range doc.Paths.Map() {
		if item.Put == nil || item.Put.Extensions["x-rest-style"] != "update" {
			continue
		}
		updates++
		patch := item.Patch
		if patch == nil {
			t.Errorf("PUT %s has no PATCH beside it", path)
			continue
		}
		if patch.RequestBody == nil ||
			patch.RequestBody.Value.Content.Get("application/merge-patch+json") == nil {
			t.Errorf("PATCH %s takes no merge patch", path)
		}
		if !takes(patch, "Idempotency-Key") {
			t.Errorf("PATCH %s takes no Idempotency-Key", path)
		}
		for _, op := range []*openapi3.Operation{item.Put, patch} {
			if !slices.Contains(divergence(op), "merge-patch") {
				t.Errorf("%s %s does not record merge-patch", op.OperationID, path)
			}
		}
		for method, op := range map[string]*openapi3.Operation{
			"GET": item.Get, "PUT": item.Put, "PATCH": patch, "DELETE": item.Delete,
		} {
			if op == nil {
				continue
			}
			if !slices.Contains(divergence(op), "if-match") {
				t.Errorf("%s %s does not record if-match", method, path)
			}
			if method != "DELETE" {
				ok := op.Responses.Status(200)
				if ok == nil || ok.Value.Headers["ETag"] == nil {
					t.Errorf("%s %s's 200 carries no ETag", method, path)
				}
			}
			if method != "GET" && (!takes(op, "If-Match") || op.Responses.Status(412) == nil) {
				t.Errorf("%s %s takes no If-Match, or answers no 412", method, path)
			}
		}
	}
	if updates < 5 {
		t.Errorf("%d update routes, want every served one", updates)
	}
}
