package api_test

import (
	"bufio"
	"bytes"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// supersessions is the register of decision 0021's supersessions: what is
// not served, and what is served although upstream deprecates it.
const supersessions = "../docs/data/supersessions.json"

// excludedFields are the root fields the Deploy reference excludes, from
// the Excluded table of its coverage.md. Every field the schema deprecates
// is among them, since generation excludes a deprecated field rather than
// routing it.
func excludedFields(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("reference/spacelift/coverage.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("^\\| `([^`]+)` \\| [a-z]+ \\| (.*) \\|$")
	out := map[string]string{}
	in := false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			in = line == "## Excluded"
			continue
		}
		if m := row.FindStringSubmatch(line); in && m != nil {
			out[m[1]] = m[2]
		}
	}
	if len(out) == 0 {
		t.Fatal("coverage.md has no Excluded rows")
	}
	return out
}

// referenceName is the reference's name for a served operationId: the
// rename register's reference name when it renames one, the same name
// otherwise.
func referenceName(id string, rows []renamed) string {
	for _, r := range rows {
		if r.owner == "" && r.infraweave == id {
			return r.reference
		}
	}
	return id
}

// deprecationViolations names each served operation that matches an
// excluded reference field without deprecated: true.
func deprecationViolations(doc *openapi3.T, excluded map[string]string, rows []renamed) []string {
	var out []string
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			reason, ok := excluded[referenceName(op.OperationID, rows)]
			if ok && !op.Deprecated {
				out = append(
					out,
					method+" "+path+" ("+op.OperationID+") is not deprecated; the reference excludes it: "+reason,
				)
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestUnitServedDeprecations fails when the API serves an operation the
// reference excludes as deprecated or superseded without marking it
// deprecated, so the weekly reference refresh cannot deprecate a served
// operation silently (decision 0021).
func TestUnitServedDeprecations(t *testing.T) {
	excluded := excludedFields(t)
	rows := renameTable(t)
	for _, v := range deprecationViolations(loadServed(t), excluded, rows) {
		t.Error(v)
	}

	doc, err := openapi3.NewLoader().LoadFromData([]byte(`openapi: 3.0.3
info: {title: fixture, version: "1"}
paths:
  /old:
    get:
      operationId: oldThing
      responses: {"200": {description: ok}}
  /old-marked:
    get:
      operationId: oldMarked
      deprecated: true
      responses: {"200": {description: ok}}
  /current:
    get:
      operationId: current
      responses: {"200": {description: ok}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := deprecationViolations(doc, map[string]string{
		"oldThing":  "deprecated upstream",
		"oldMarked": "deprecated upstream",
	}, rows)
	want := []string{
		"GET /old (oldThing) is not deprecated; the reference excludes it: deprecated upstream",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// deprecatedEnumValues are the enum values the schema snapshot marks
// deprecated or describes as no longer used, by GraphQL type.
func deprecatedEnumValues(t *testing.T) map[string][]string {
	t.Helper()
	var snap struct {
		Data struct {
			Schema struct {
				Types []struct {
					Kind, Name string
					EnumValues []struct {
						Name         string
						Description  string
						IsDeprecated bool
					}
				}
			} `json:"__schema"` //nolint:tagliatelle // fixed by the GraphQL specification
		}
	}
	readRegister(t, "reference/spacelift/introspection.json", &snap)
	out := map[string][]string{}
	for _, ty := range snap.Data.Schema.Types {
		for _, v := range ty.EnumValues {
			if v.IsDeprecated ||
				strings.Contains(strings.ToLower(v.Description), "no longer used") {
				out[ty.Name] = append(out[ty.Name], v.Name)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("the schema snapshot deprecates no enum value")
	}
	return out
}

// liveDeprecated are the Type.VALUE names that served records of the
// supersession register keep live although upstream deprecates them.
func liveDeprecated(t *testing.T) map[string]bool {
	t.Helper()
	var reg struct {
		Records []struct {
			Treatment, Interface string
		}
	}
	readRegister(t, supersessions, &reg)
	dotted := regexp.MustCompile("`([A-Za-z0-9_]+\\.[A-Z0-9_]+)`")
	out := map[string]bool{}
	for _, r := range reg.Records {
		if r.Treatment != "served" {
			continue
		}
		for _, m := range dotted.FindAllStringSubmatch(r.Interface, -1) {
			out[m[1]] = true
		}
	}
	return out
}

// enumViolations names each deprecated value a served component schema
// still lists. A served schema is matched to its GraphQL enum by
// x-graphql-type, or by its own name when it has none.
func enumViolations(
	doc *openapi3.T,
	deprecated map[string][]string,
	live map[string]bool,
) []string {
	var out []string
	for name, ref := range doc.Components.Schemas {
		s := ref.Value
		if s == nil || len(s.Enum) == 0 {
			continue
		}
		gql := name
		if v, ok := s.Extensions["x-graphql-type"].(string); ok {
			gql = v
		}
		for _, v := range s.Enum {
			value, _ := v.(string)
			if slices.Contains(deprecated[gql], value) && !live[gql+"."+value] {
				out = append(out, name+" serves "+gql+"."+value+", which upstream deprecates")
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestUnitSupersededEnums fails when a served enum keeps a value the schema
// deprecates or no longer uses, unless the supersession register keeps it
// live (decision 0021).
func TestUnitSupersededEnums(t *testing.T) {
	deprecated := deprecatedEnumValues(t)
	live := liveDeprecated(t)
	if !live["TerraformWorkflowTool.OPEN_TOFU"] {
		t.Errorf("%s: DEP-02's TerraformWorkflowTool.OPEN_TOFU is not read as live", supersessions)
	}
	for _, v := range enumViolations(loadServed(t), deprecated, live) {
		t.Error(v)
	}

	doc, err := openapi3.NewLoader().LoadFromData([]byte(`openapi: 3.0.3
info: {title: fixture, version: "1"}
paths: {}
components:
  schemas:
    RunState:
      type: string
      enum: [QUEUED, PENDING]
    Tool:
      type: string
      enum: [TERRAFORM, OPEN_TOFU]
      x-graphql-type: TerraformWorkflowTool
    PolicyKind:
      type: string
      enum: [PLAN, TERRAFORM_PLAN]
      x-graphql-type: PolicyType
`))
	if err != nil {
		t.Fatal(err)
	}
	got := enumViolations(doc, map[string][]string{
		"RunState":              {"PENDING"},
		"TerraformWorkflowTool": {"OPEN_TOFU"},
		"PolicyType":            {"TERRAFORM_PLAN"},
	}, map[string]bool{"TerraformWorkflowTool.OPEN_TOFU": true})
	want := []string{
		"PolicyKind serves PolicyType.TERRAFORM_PLAN, which upstream deprecates",
		"RunState serves RunState.PENDING, which upstream deprecates",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
