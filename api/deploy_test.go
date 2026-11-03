package api_test

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/infraweave/api"
	"github.com/weaveplatform/infraweave/internal/openapibundle"
)

// conventions is decision 0003, whose table holds the slugs its spec
// components use.
const conventions = "../docs/research/decisions/0003-api-conventions.md"

// problems is decision 0022, whose table is the complete list of problem
// slugs. Its rows for 0003's slugs must keep 0003's status.
const problems = "../docs/research/decisions/0022-api-errors-and-update-semantics.md"

const problemPrefix = "urn:infraweave:problem:"

// TestUnitDeployBundleCurrent fails when a fragment under src/deploy changes
// without `make bundle`, so the committed and embedded document never drifts
// from what the fragments say.
func TestUnitDeployBundleCurrent(t *testing.T) {
	want, err := openapibundle.Bundle("src/deploy")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(api.Deploy, want) {
		t.Error("api/deploy.openapi.yaml is stale; run make bundle and commit the result")
	}
}

func loadDeploy(t *testing.T) *openapi3.T {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Deploy)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestUnitDeployConventionComponents checks that every component decision
// 0003 names exists and says what it is for.
func TestUnitDeployConventionComponents(t *testing.T) {
	c := loadDeploy(t).Components
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

// slugTable reads a decision record's problem table: every row that starts
// with a backticked slug and a three-digit status.
func slugTable(t *testing.T, record string) map[string]int {
	t.Helper()
	text, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]int{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\| ([0-9]{3}) \\|").
		FindAllStringSubmatch(string(text), -1) {
		listed[m[1]], _ = strconv.Atoi(m[2])
	}
	if len(listed) == 0 {
		t.Fatalf("%s lists no problem slugs", record)
	}
	return listed
}

// TestUnitDeployProblemTypes checks that every problem example in the spec
// uses a slug decision 0022 lists, with the status the record gives it, so
// that record stays the one complete list of problem types. It also checks
// that every slug decision 0003 defines is used and has the same status in
// both records.
func TestUnitDeployProblemTypes(t *testing.T) {
	listed := slugTable(t, problems)
	used := map[string]bool{}
	for name, r := range loadDeploy(t).Components.Responses {
		mt := r.Value.Content.Get("application/problem+json")
		ex, ok := mt.Example.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := ex["type"].(string)
		slug, found := strings.CutPrefix(typ, problemPrefix)
		status, known := listed[slug]
		switch {
		case !found || !known:
			t.Errorf("response %s: type %q is not a slug listed in %s", name, typ, problems)
		case ex["status"] != float64(status):
			t.Errorf("response %s: status %v, record says %d", name, ex["status"], status)
		}
		used[slug] = true
	}
	for slug, status := range slugTable(t, conventions) {
		if !used[slug] {
			t.Errorf("%s lists %s, but no response uses it", conventions, slug)
		}
		if listed[slug] != status {
			t.Errorf("%s gives %s status %d, %s gives %d", conventions, slug, status, problems, listed[slug])
		}
	}
}

// naming is decision 0001, whose rename table maps each reference name to
// the name infraweave serves in its place.
const naming = "../docs/research/decisions/0001-rest-first-spacelift-surface.md"

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
// names a client uses.
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

// renamed is one identifier row of decision 0001's rename table. pattern
// matches the reference name, with * standing for any run of characters;
// owner, when set, limits the match to names that component schema holds.
type renamed struct {
	owner, reference, infraweave string
	pattern                      *regexp.Regexp
}

// renameTable reads the table under 0001's "### Rename table" heading: every
// row whose two cells are each one code span. A reference cell `Schema.name`
// names a property or enum value of that schema only, so a common word such
// as organizationID is refused only where it means what the reference meant.
// Rows that describe a name in words are concepts, not identifiers, and are
// skipped.
func renameTable(t *testing.T) []renamed {
	t.Helper()
	text, err := os.ReadFile(naming)
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(text), "\n### Rename table\n")
	if !found {
		t.Fatalf("%s has no ### Rename table heading", naming)
	}
	if end := strings.Index(section, "\n#"); end >= 0 {
		section = section[:end]
	}
	qualified := regexp.MustCompile(`^([A-Z][A-Za-z0-9]*)\.([A-Za-z0-9_]+)$`)
	var rows []renamed
	for _, m := range regexp.MustCompile("(?m)^\\| `([^`]+)` \\| `([^`]+)` \\|$").
		FindAllStringSubmatch(section, -1) {
		r := renamed{reference: m[1], infraweave: m[2]}
		name := m[1]
		if q := qualified.FindStringSubmatch(name); q != nil {
			r.owner, name = q[1], q[2]
		}
		r.pattern = regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(name), `\*`, ".*") + "$")
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatalf("%s has no identifier rows under ### Rename table", naming)
	}
	return rows
}

func spaceliftViolations(names []servedName) []string {
	var found []string
	for _, n := range names {
		if strings.Contains(strings.ToLower(n.name), "spacelift") {
			found = append(found, n.where+": "+n.name+" contains spacelift")
		}
	}
	return found
}

func renameViolations(names []servedName, rows []renamed) []string {
	var found []string
	for _, n := range names {
		for _, r := range rows {
			if (r.owner == "" || r.owner == n.owner) && r.pattern.MatchString(n.name) {
				found = append(found, n.where+": "+n.name+" is the reference name "+
					r.reference+", which 0001 renames to "+r.infraweave)
			}
		}
	}
	return found
}

// servedNamesFixture serves one reference name, so the test proves that a
// scaffold copied from the reference fails both checks rather than passing
// because the walker never reached its names.
const servedNamesFixture = `openapi: 3.0.3
info: {title: fixture, version: "1"}
paths: {}
components:
  schemas:
    Installation:
      type: object
      properties:
        spaceliftAwsAccountId: {type: string}
`

// TestUnitServedNames fails when the deploy API serves a name that says
// spacelift in any case, or a reference name that decision 0001's rename
// table replaces. Families are scaffolded from Spacelift's spec, so each
// arrives carrying Spacelift's names; a scaffold that keeps one would make
// infraweave's clients, SDK and provider depend on a vendor's name. The
// spacelift check catches a rename the table lacks; the table check catches
// reference names that never said spacelift, such as the non-root admin
// settings. Descriptions are prose and are left to review.
func TestUnitServedNames(t *testing.T) {
	rows := renameTable(t)
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.infraweave), "spacelift") {
			t.Errorf("%s renames %s to %s, which contains spacelift", naming, r.reference, r.infraweave)
		}
	}
	t.Run("deploy", func(t *testing.T) {
		names := servedNames(loadDeploy(t))
		if len(names) == 0 {
			t.Fatal("the deploy API serves no names")
		}
		for _, v := range append(spaceliftViolations(names), renameViolations(names, rows)...) {
			t.Error(v)
		}
	})
	t.Run("fixture", func(t *testing.T) {
		doc, err := openapi3.NewLoader().LoadFromData([]byte(servedNamesFixture))
		if err != nil {
			t.Fatal(err)
		}
		names := servedNames(doc)
		if len(spaceliftViolations(names)) != 1 {
			t.Errorf("spacelift check found %q, want spaceliftAwsAccountId", spaceliftViolations(names))
		}
		if len(renameViolations(names, rows)) != 1 {
			t.Errorf("rename check found %q, want spaceliftAwsAccountId", renameViolations(names, rows))
		}
	})
}
