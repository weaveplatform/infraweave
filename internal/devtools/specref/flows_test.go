package specref

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
)

func TestUnitFlowsProviderExtract(t *testing.T) {
	snap, err := ReadFlowsProviderTarball(flowsProviderTarball(t))
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(snap.RPCs))
	byPath := map[string]RPC{}
	for _, r := range snap.RPCs {
		paths = append(paths, r.Path)
		byPath[r.Path] = r
	}
	want := []string{
		"/provider/flows/export_definition", "/provider/organization/read_secret",
		"/provider/projects/list", "/provider/widgets/attach_to_flow",
		"/provider/widgets/confirm_widget", "/provider/widgets/create",
		"/provider/widgets/delete", "/provider/widgets/detach_from_flow",
		"/provider/widgets/find", "/provider/widgets/get", "/provider/widgets/update",
	}
	if d := cmp.Diff(want, paths); d != "" {
		t.Errorf("paths (-want +got):\n%s", d)
	}
	create := byPath["/provider/widgets/create"].Request
	if d := cmp.Diff([]string{"Untagged", "name", "projectId", "style", "tags"}, create.Required); d != "" {
		t.Errorf("required: omitempty fields are optional (-want +got):\n%s", d)
	}
	for name, want := range map[string]Shape{
		"limit":    {Type: "integer", Format: "int64", Nullable: true},
		"style":    {Ref: "Style", Nullable: true},
		"Untagged": {Type: "boolean"},
	} {
		if d := cmp.Diff(want, *create.Properties[name]); d != "" {
			t.Errorf("%s (-want +got):\n%s", name, d)
		}
	}
	if _, ok := create.Properties["internal"]; ok {
		t.Error("an unexported field is not sent")
	}
	if _, ok := create.Properties["Skipped"]; ok {
		t.Error(`json:"-" drops a field`)
	}
	got := byPath["/provider/widgets/get"].Response.Properties
	for name, want := range map[string]string{
		"ratio": "number", "count": "integer", "created": "string", "raw": "", "extra": "", "meta": "",
	} {
		if got[name].Type != want {
			t.Errorf("%s is %q, want %q", name, got[name].Type, want)
		}
	}
	if got["created"].Format != "date-time" || !got["reset"].Nullable || got["parts"].Items.Ref != "Part" {
		t.Errorf("response = %+v", got)
	}
	if !byPath["/provider/widgets/delete"].Response.isEmpty() {
		t.Error("struct{} is an empty response")
	}
	if d := cmp.Diff([]string{"Part", "Project", "Style"}, sortedKeys(snap.Types)); d != "" {
		t.Errorf("named types (-want +got):\n%s", d)
	}
}

func TestUnitFlowsProviderErrors(t *testing.T) {
	source := func(body string) []byte {
		return tarball(t, map[string]string{
			"internal/provider/client.go": flowsProviderClient,
			"internal/provider/x.go":      "package provider\n" + body,
		})
	}
	for name, tc := range map[string]struct {
		tgz  []byte
		want string
	}{
		"no package": {tarball(t, map[string]string{"main.go": "package main"}), "no Go files"},
		"parse":      {source("func {"), "parse internal/provider/x.go"},
		"stray path": {source(`var p = "/provider/x/get"`), "is not passed to CallFlowsAPI"},
		"computed path": {
			source(`func f(c Config) { CallFlowsAPI[struct{}, struct{}](c, "/provider/" + "x", struct{}{}) }`),
			"not a string literal or constant",
		},
		"two signatures": {
			source(`func f(c Config) {
	CallFlowsAPI[struct{}, struct{}](c, "/provider/x/get", struct{}{})
	CallFlowsAPI[string, struct{}](c, "/provider/x/get", "")
}`), "is called with",
		},
		"bad request": {
			source(`type R struct{ C chan int ` + "`json:\"c\"`" + ` }
func f(c Config) { CallFlowsAPI[R, struct{}](c, "/provider/x/get", R{}) }`), "has no JSON shape",
		},
		"bad response": {
			source(`func f(c Config) { CallFlowsAPI[struct{}, map[int]string](c, "/provider/x/get", struct{}{}) }`),
			"has no JSON shape",
		},
		"bad nested": {
			source(`type N struct{ F func() ` + "`json:\"f\"`" + ` }
type R struct{ N []*N ` + "`json:\"n\"`" + ` }
func f(c Config) { CallFlowsAPI[R, struct{}](c, "/provider/x/get", R{}) }`), "has no JSON shape",
		},
		"bad selector": {
			source(`func f(c Config) { CallFlowsAPI[struct{ D os.File }, struct{}](c, "/provider/x/get", nil) }`),
			"has no JSON shape",
		},
		"not a tarball": {[]byte("nope"), "open tarball"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadFlowsProviderTarball(tc.tgz)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUnitFlowctlExtract(t *testing.T) {
	snap, err := ReadFlowctlAPI([]byte(flowctlFixture))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]RPC{}
	for _, r := range snap.RPCs {
		byPath[r.Path] = r
	}
	if len(byPath) != 8 {
		t.Fatalf("rpcs = %v", sortedKeys(byPath))
	}
	projects := byPath["/cli/projects"]
	if !projects.Unenveloped || projects.Response.Properties["projects"].Items.Properties["name"].Type != "string" {
		t.Errorf("projects = %+v", projects)
	}
	create := byPath["/cli/widgets/create_widget"]
	style := create.Request.Properties["style"]
	if create.Request.Properties["name"].Type != "string" || style.Properties["size"].Type != "number" ||
		style.Properties["round"].Type != "boolean" || style.Properties["label"].Type != "string" {
		t.Errorf("create request = %+v", create.Request)
	}
	if create.Response.Properties["widget"].Properties["id"].Type != "" {
		t.Error("a read without a cast is any")
	}
	update := byPath["/cli/widgets/update"].Request
	if d := cmp.Diff([]string{"id"}, update.Required); d != "" || len(update.Properties) != 3 {
		t.Errorf("the two update calls merge, required where both send it: %+v (%s)", update, d)
	}
	export := byPath["/cli/flows/export"].Request
	if d := cmp.Diff([]string{"flowId", "omitIds", "omitLayout"}, sortedKeys(export.Properties)); d != "" {
		t.Errorf("...options spreads ExportOptions (-want +got):\n%s", d)
	}
	if byPath["/cli/flows/export"].Response.Properties["data"].Type != "string" {
		t.Error("res.data.data as string")
	}
	run := byPath["/cli/flows/start_test_scenario_run"].Response
	if run.Properties["runs"].Items.Ref != "Run" {
		t.Errorf("res.data as {...} = %+v", run)
	}
	if byPath["/cli/flows/start_test_scenario_run_batch"].Response.Properties["ids"].Items.Type != "string" {
		t.Error("res?.data.ids as string[]")
	}
	r := snap.Types["Run"]
	if !r.Properties["error"].Nullable || r.Properties["status"].Ref != "RunStatus" ||
		r.Properties["labels"].Values.Type != "string" || r.Properties["steps"].Items.Properties["ok"].Type != "boolean" ||
		r.Properties["scores"].Items.Type != "number" || contains(r.Required, "quoted-key") {
		t.Errorf("Run = %+v", r)
	}
	if d := cmp.Diff([]string{"queued", "passed", "failed"}, snap.Types["RunStatus"].Enum); d != "" {
		t.Errorf("RunStatus (-want +got):\n%s", d)
	}
}

func TestUnitFlowctlErrors(t *testing.T) {
	fn := func(body string) string {
		return "export async function f(a: string, o: Opts) {\n" + body + "\n}\n"
	}
	for name, tc := range map[string]struct{ src, want string }{
		"stray path":       {`const p = "/cli/x/get";`, "is not passed to apiFetch"},
		"stray in body":    {fn(`const p = "/cli/x/get";`), "is not passed to apiFetch"},
		"computed path":    {fn(`apiFetch(p, {});`), "not a string literal"},
		"no body":          {fn(`apiFetch("/cli/x/get");`), `want ","`},
		"unclosed call":    {fn(`apiFetch("/cli/x/get", {} x);`), `want ")"`},
		"not a param":      {fn(`apiFetch("/cli/x/get", { b });`), "b is not a parameter"},
		"untyped value":    {fn(`apiFetch("/cli/x/get", { b: a + 1 });`), `have "+"`},
		"call value":       {fn(`apiFetch("/cli/x/get", { b: g() });`), "cannot type the value"},
		"bad key":          {fn(`apiFetch("/cli/x/get", { [k]: a });`), "want a member name"},
		"bad spread":       {fn(`apiFetch("/cli/x/get", { ...1 });`), "want a name"},
		"spread non-iface": {"type Opts = string;\n" + fn(`apiFetch("/cli/x/get", { ...o });`), "is not an interface"},
		"generic":          {"interface A { m: Map<string, string> }", "generic type Map"},
		"mixed union":      {"type U = \"a\" | number;", "not a string enum"},
		"record key":       {"type R = Record<number, string>;", "Record keys must be string"},
		"array arity":      {"type R = Array<string, number>;", `want ">"`},
		"record arity":     {"type R = Record<string>;", `want ","`},
		"bad type":         {"type R = ;", "want a type"},
		"paren":            {"type R = (string;", `want ")"`},
		"no equals":        {"type R string;", `want "="`},
		"no name":          {"interface { }", "want a name"},
		"no colon":         {"interface A { b string }", `want ":"`},
		"bad member":       {"interface A { 1: string }", "want a member name"},
		"cast":             {fn(`const r = await apiFetch("/cli/x/get", {}); r.data as string;`), "not an object"},
		"bad cast":         {fn(`const r = await apiFetch("/cli/x/get", {}); r.data.x as ;`), "want a type"},
		"bad read":         {fn(`const r = await apiFetch("/cli/x/get", {}); r.1;`), "want a name"},
		"comment":          {"/* open", "unterminated comment"},
		"string":           {`const s = "open`, "unterminated string"},
		"no body fn":       {"export async function f(a: string)", "want a function body"},
		"bad param":        {"export async function f(1) {}", "want a name"},
		"bad param type":   {"export async function f(a: ) {}", "want a type"},
		"unbalanced":       {"export async function f() { (", "unbalanced"},
		"bad fn name":      {"export async function (a) {}", "want a name"},
		"bad iface body":   {"interface A { b: ; }", "want a type"},
		"bad alias":        {"type A = string | ;", "want a type"},
		"two signatures":   {fn(`apiFetch("/cli/x/get", { a });`) + strings.Replace(fn(`apiFetch("/cli/x/get", { a: 1 });`), "f(", "g(", 1), "is string in one client and number"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadFlowctlAPI([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrFlowsSource) {
				t.Errorf("%v is not ErrFlowsSource", err)
			}
		})
	}
}

func TestUnitSDKIdentifiers(t *testing.T) {
	ids, err := ReadSDKIdentifiers([]byte(flowsSDKFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []SDKIdentifier{
		{Name: "AppSchema", Kind: "interface", Exported: true, Members: []SDKMember{
			{Name: "name", Optional: true}, {Name: "blocks"}, {Name: "onSync", Optional: true},
			{Name: "quoted"}, {Name: "generic"},
		}},
		{Name: "KVPair", Kind: "interface", Members: []SDKMember{{Name: "key"}, {Name: "value", Optional: true}}},
		{Name: "Shape", Kind: "type"},
		{Name: "Status", Kind: "type", Exported: true},
		{Name: "defineApp", Kind: "function", Exported: true},
		{Name: "kv", Kind: "const", Exported: true, Members: []SDKMember{{Name: "get"}, {Name: "list"}}},
		{Name: "plain", Kind: "const"},
	}
	if d := cmp.Diff(want, ids); d != "" {
		t.Errorf("identifiers (-want +got):\n%s", d)
	}
	for name, src := range map[string]string{
		"lex":         `"open`,
		"interface":   "interface { }",
		"export list": "export { 1 };",
		"export open": "export { a",
		"body":        "interface A { (",
	} {
		if _, err := ReadSDKIdentifiers([]byte(src)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestUnitFlowsMappingParse(t *testing.T) {
	if _, err := ParseFlowsMapping([]byte(flowsMappingFixture)); err != nil {
		t.Fatal(err)
	}
	base := "tags: {T: t}\nresources:\n  r: {collection: rs, id: rId, tag: T}\n"
	for name, tc := range map[string]struct{ yaml, want string }{
		"unknown key":    {"nope: 1\n", "field nope not found"},
		"no collection":  {"tags: {T: t}\nresources: {r: {id: x, tag: T}}\n", "needs collection and id"},
		"undescribed":    {"tags: {}\nresources: {r: {collection: rs, id: x, tag: T}}\n", "which tags does not describe"},
		"bad parent":     {base + "  s: {collection: ss, id: k, parent: nope, tag: T}\n", "unknown parent nope"},
		"bad noun":       {base + "nouns: {x: nope}\n", "names unknown resource nope"},
		"no operationId": {base + "rpcs: {/a/b/get: {}}\n", "has no operationId"},
		"half override":  {base + "rpcs: {/a/b/get: {operationId: o, method: GET}}\n", "needs both method and path"},
		"no resource":    {base + "rpcs: {/a/b/get: {operationId: o, method: GET, path: /x}}\n", "needs its resource"},
		"bad resource":   {base + "rpcs: {/a/b/get: {operationId: o, resource: nope}}\n", "names unknown resource"},
		"bad tag":        {base + "rpcs: {/a/b/get: {operationId: o, tag: U}}\n", `has tag "U"`},
		"both":           {base + "rpcs: {/a/b/get: {operationId: o}}\nexclude: {/a/b/get: why}\n", "both mapped and excluded"},
		"no reason":      {base + "exclude: {/a/b/get: ' '}\n", "needs a reason"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFlowsMapping([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUnitFlowsRoutes(t *testing.T) {
	m, err := ParseFlowsMapping([]byte(flowsMappingFixture))
	if err != nil {
		t.Fatal(err)
	}
	req := func(keys ...string) *Shape {
		s := objectShape()
		for _, k := range keys {
			s.Properties[k] = &Shape{Type: "string"}
		}
		return s
	}
	for rpc, tc := range map[string]struct {
		req   *Shape
		route string
		bind  map[string]string
		code  int
	}{
		"/provider/widgets/create":                 {req("projectId", "name"), "POST /flows/projects/{projectId}/widgets", map[string]string{"projectId": "projectId"}, 201},
		"/provider/widgets/get":                    {req("widgetId"), "GET /flows/widgets/{widgetId}", map[string]string{"widgetId": "widgetId"}, 0},
		"/provider/widgets/update":                 {req("id"), "PATCH /flows/widgets/{widgetId}", map[string]string{"widgetId": "id"}, 0},
		"/provider/widgets/confirm_widget":         {req("id"), "POST /flows/widgets/{widgetId}/confirm", map[string]string{"widgetId": "id"}, 0},
		"/provider/widgets/attach_to_flow":         {req("widgetId", "flowId"), "PUT /flows/flows/{flowId}/widgets/{widgetId}", map[string]string{"flowId": "flowId", "widgetId": "widgetId"}, 0},
		"/provider/widgets/detach_from_flow":       {req("widgetId", "flowId"), "DELETE /flows/flows/{flowId}/widgets/{widgetId}", map[string]string{"flowId": "flowId", "widgetId": "widgetId"}, 0},
		"/cli/widgets/list_widgets":                {req("projectId"), "GET /flows/projects/{projectId}/widgets", map[string]string{"projectId": "projectId"}, 200},
		"/provider/organization/read_secret":       {req("projectId", "key"), "GET /flows/projects/{projectId}/secrets/{key}", map[string]string{"projectId": "projectId", "key": "key"}, 0},
		"/cli/flows/start_test_scenario_run":       {req("scenarioId"), "POST /flows/test-scenarios/{testScenarioId}/runs", map[string]string{"testScenarioId": "scenarioId"}, 201},
		"/cli/flows/start_test_scenario_run_batch": {req("flowId"), "POST /flows/flows/{flowId}/test-scenario-runs/batch", map[string]string{"flowId": "flowId"}, 200},
		"/provider/projects/list":                  {req(), "GET /flows/projects", map[string]string{}, 200},
		"/provider/widgets/find":                   {req("name"), "GET /flows/widgets", map[string]string{}, 0},
	} {
		t.Run(rpc, func(t *testing.T) {
			r, err := m.route(rpc, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Method + " " + r.Path; got != tc.route || r.Code != tc.code {
				t.Errorf("route = %s (%d), want %s (%d)", got, r.Code, tc.route, tc.code)
			}
			if d := cmp.Diff(tc.bind, r.Bind); d != "" {
				t.Errorf("bind (-want +got):\n%s", d)
			}
		})
	}
	for rpc, tc := range map[string]struct {
		req  *Shape
		want string
	}{
		"/cli/x":                           {req(), "has no verb"},
		"/provider/widgets/frobnicate":     {req(), "no known verb"},
		"/provider/gadgets/get":            {req(), "no resource for the noun gadgets"},
		"/provider/widgets/attach_to_moon": {req(), "no resource for the noun moon"},
		"/provider/moons/attach_to_flow":   {req(), "no resource for the noun moons"},
		"/provider/widgets/create":         {req("projectId", "flowId"), "the parent is ambiguous"},
		"/provider/widgets/delete":         {req("name"), "no request property carries {widgetId}"},
	} {
		t.Run(rpc, func(t *testing.T) {
			_, err := m.route(rpc, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestUnitFlowsGenerateGolden(t *testing.T) {
	g := generateFlowsFixture(t)
	golden(t, "flows.openapi.yaml", g.Spec)
	golden(t, "flows-coverage.md", g.Coverage)
	if g.Summary != "19 RPC paths (11 provider, 8 flowctl): 15 operations, 1 excluded" {
		t.Errorf("summary = %q", g.Summary)
	}
	doc, err := openapi3.NewLoader().LoadFromData(g.Spec)
	if err != nil {
		t.Fatal(err)
	}
	export := doc.Paths.Value("/flows/flows/{flowId}/definition").Get
	rpcs, _ := json.Marshal(export.Extensions["x-rpc"])
	if string(rpcs) != `[{"client":"flowctl","path":"/cli/flows/export"},{"client":"provider","path":"/provider/flows/export_definition"}]` {
		t.Errorf("x-rpc = %s", rpcs)
	}
	def := export.Responses.Status(200).Value.Content.Get(jsonMedia).Schema.Value.Properties["definition"].Value
	if def.Extensions["x-yaml-schema"] != "#/components/schemas/FlowDefinition" {
		t.Errorf("definition = %+v", def)
	}
	update := doc.Paths.Value("/flows/widgets/{widgetId}").Patch
	if update.Parameters[0].Value.Extensions["x-rpc-field"] != "id" ||
		update.Responses.Status(204) == nil {
		t.Errorf("update = %+v", update)
	}
	find := doc.Paths.Value("/flows/widgets").Get
	if len(find.Parameters) != 1 || find.Parameters[0].Value.In != "query" || !find.Parameters[0].Value.Required {
		t.Errorf("find = %+v", find.Parameters)
	}
	if err := Flows.CheckDir(context.Background(), writeFlowsFixtureDirWith(t, FileFlowsSpec, "stale")); !errors.Is(err, ErrDrift) {
		t.Errorf("stale spec: %v", err)
	}
}

// writeFlowsFixtureDirWith is the fixture directory, freshly generated, with
// one file replaced.
func writeFlowsFixtureDirWith(t *testing.T, name, content string) string {
	t.Helper()
	dir := writeFlowsFixtureDir(t)
	if _, err := Flows.GenerateDir(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), filePerm); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUnitFlowsGenerateErrors(t *testing.T) {
	mapping := func(edit func(string) string) string { return edit(flowsMappingFixture) }
	for name, tc := range map[string]struct {
		file, content, want string
	}{
		"unmapped": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "  /provider/widgets/delete: {operationId: deleteWidget}\n", "", 1)
		}), "/provider/widgets/delete (provider) is not mapped"},
		"stale": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "rpcs:\n", "rpcs:\n  /provider/widgets/gone: {operationId: gone}\n", 1)
		}), "/provider/widgets/gone is mapped but no client calls it"},
		"stale exclude": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "exclude:\n", "exclude:\n  /cli/gone: Gone.\n", 1)
		}), "/cli/gone is excluded but no client calls it"},
		"route clash": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "{operationId: deleteWidget}", "{operationId: getWidget}", 1)
		}), "maps it to DELETE /flows/widgets/{widgetId}"},
		"same route": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "path: /flows/widgets, resource: widget}", "path: '/flows/widgets/{widgetId}', resource: widget}", 1)
		}), "no request property carries {widgetId}"},
		"two operations": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "{operationId: confirmWidget}", "{operationId: confirmWidget, method: GET, path: '/flows/widgets/{widgetId}', resource: widget}", 1)
		}), "two operations share GET /flows/widgets/{widgetId}"},
		"query object": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "{operationId: createWidget}", "{operationId: createWidget, method: GET, path: /flows/widgets/all, resource: widget}", 1)
		}), "labels (object) cannot be a query parameter"},
		"merge conflict": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "{operationId: getSecret}", "{operationId: listProjects, method: GET, path: /flows/projects/all, resource: project}", 1)
		}), "maps listProjects to GET /flows/projects/all (Flows Projects), but"},
		"bad yaml schema": {FileMapping, mapping(func(s string) string {
			return strings.Replace(s, "definition: FlowDefinition", "definition: Nope", 1)
		}), "the schema Nope, which schemas.yaml lacks"},
		"overlay clash": {FileFlowsOverlay, flowsOverlayFixture + "Style: {type: object}\n", "names both a client type and an overlay schema"},
		"bad overlay":   {FileFlowsOverlay, "A: {type: nope}\n", "validate schemas.yaml"},
		"overlay yaml":  {FileFlowsOverlay, "A: [\n", "schemas.yaml"},
		"bad lock":      {FileLock, "nope: 1\n", FileLock},
		"bad provider":  {FileFlowsProvider, "{", "decode " + FileFlowsProvider},
		"bad mapping":   {FileMapping, "nope: 1\n", FileMapping},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeFlowsFixtureDir(t)
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(tc.content), filePerm); err != nil {
				t.Fatal(err)
			}
			_, err := Flows.Generate(context.Background(), dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	for _, name := range []string{FileLock, FileFlowsFlowctl, FileMapping, FileFlowsOverlay} {
		dir := writeFlowsFixtureDir(t)
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := Flows.Generate(context.Background(), dir); err == nil {
			t.Errorf("missing %s: want an error", name)
		}
	}
	if _, err := Flows.Generate(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory: want an error")
	}
}

func TestUnitFlowsTypeClash(t *testing.T) {
	src := &FlowsSources{
		Provider: &RPCSnapshot{Types: map[string]*Shape{"T": {Type: "string"}}},
		Flowctl:  &RPCSnapshot{Types: map[string]*Shape{"T": {Type: "number"}}},
		Mapping:  &FlowsMapping{},
	}
	if err := addFlowsSchemas(&openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{}}}, src); err == nil ||
		!strings.Contains(err.Error(), "both clients declare a different T") {
		t.Errorf("err = %v", err)
	}
}

func TestUnitFlowsShapes(t *testing.T) {
	ref := (&Shape{Ref: "T", Nullable: true}).schema().Value
	if !ref.Nullable || ref.AllOf[0].Ref != "#/components/schemas/T" {
		t.Errorf("nullable ref = %+v", ref)
	}
	if _, err := merged(&Shape{Type: "object", Properties: map[string]*Shape{"a": {Ref: "A"}}},
		&Shape{Type: "object", Properties: map[string]*Shape{"a": {}}}); err == nil ||
		!strings.Contains(err.Error(), "A in one client and any") {
		t.Errorf("merge: %v", err)
	}
	if s, err := merged(nil, &Shape{Type: "string"}); err != nil || s.Type != "string" {
		t.Errorf("merge into nothing: %v %v", s, err)
	}
	if got := untyped(nil, ""); got != nil {
		t.Errorf("untyped(nil) = %v", got)
	}
	if annotateYAML(nil, &FlowsMapping{}) != nil {
		t.Error("annotateYAML(nil)")
	}
	if summaryWords("getFlowDefinition") != "Get flow definition" {
		t.Error(summaryWords("getFlowDefinition"))
	}
	if (&FlowsLock{Provider: LockRepo{Ref: "abc"}, Flowctl: LockRepo{Ref: "def"}}).version() != "provider-abc.flowctl-def" {
		t.Error("short refs stay whole")
	}
}

func TestUnitValidateFlowDefinition(t *testing.T) {
	overlay, err := ParseFlowsOverlay([]byte(flowsOverlayFixture))
	if err != nil {
		t.Fatal(err)
	}
	ok := "blocks:\n  - &b\n    name: A\n    config:\n      when: !expr outputs.x > 1\n  - *b\n"
	if err := ValidateFlowDefinition(overlay, []byte(ok)); err != nil {
		t.Errorf("valid flow: %v", err)
	}
	if err := ValidateFlowDefinition(overlay, nil); err == nil {
		t.Error("an empty document is not an object")
	}
	for name, tc := range map[string]struct{ flow, want string }{
		"unknown key": {"blocks: []\nextra: 1\n", "does not match schemas.yaml: FlowDefinition"},
		"no name":     {"blocks:\n  - config: {}\n", `/blocks/0`},
		"yaml":        {"blocks: [\n", "decode flow"},
		"scalar":      {"blocks: !!int x\n", "line 1"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateFlowDefinition(overlay, []byte(tc.flow))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := validateJSON(overlay, "Nope", []byte("{}")); !errors.Is(err, ErrFlowsOverlay) {
		t.Errorf("unknown schema: %v", err)
	}
	if err := validateJSON(overlay, "AppVersions", []byte("{")); err == nil {
		t.Error("not JSON: want an error")
	}
}

func TestUnitFlowsInventory(t *testing.T) {
	dir := writeFlowsFixtureDir(t)
	register := filepath.Join(t.TempDir(), "flows-sdk-inventory.json")
	out := InventoryOutputs{Register: register}
	if err := Flows.Inventory(dir, out, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(register)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "flows-sdk-inventory.json", got)
	if err := Flows.Inventory(dir, out, true); err != nil {
		t.Errorf("check after write: %v", err)
	}
	if err := os.WriteFile(register, []byte("{}"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := Flows.Inventory(dir, out, true); !errors.Is(err, ErrDrift) {
		t.Errorf("stale register: %v", err)
	}
	for name, tc := range map[string]struct {
		dir string
		out InventoryOutputs
	}{
		"report":      {dir, InventoryOutputs{Report: "r.md", Register: register}},
		"no register": {dir, InventoryOutputs{}},
		"no dir":      {filepath.Join(t.TempDir(), "missing"), out},
		"unwritable":  {dir, InventoryOutputs{Register: filepath.Join(t.TempDir(), "missing", "r.json")}},
	} {
		if err := Flows.Inventory(tc.dir, tc.out, false); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	for file, content := range map[string]string{FileFlowsSDK: "{", FileLock: "nope: 1\n"} {
		d := writeFlowsFixtureDir(t)
		if err := os.WriteFile(filepath.Join(d, file), []byte(content), filePerm); err != nil {
			t.Fatal(err)
		}
		if err := Flows.Inventory(d, out, false); err == nil {
			t.Errorf("bad %s: want an error", file)
		}
	}
	for _, file := range []string{FileFlowsSDK, FileLock} {
		d := writeFlowsFixtureDir(t)
		if err := os.Remove(filepath.Join(d, file)); err != nil {
			t.Fatal(err)
		}
		if err := Flows.Inventory(d, out, false); err == nil {
			t.Errorf("missing %s: want an error", file)
		}
	}
}

func TestUnitFlowsPins(t *testing.T) {
	dir := writeFlowsFixtureDir(t)
	pins, err := Flows.Pins(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"flows-provider-repo": "o/terraform-provider-flows",
		"flows-provider-ref":  "1111111111111111111111111111111111111111",
		"flowctl-repo":        "o/flowctl",
		"flowctl-ref":         "2222222222222222222222222222222222222222",
		"sdk-package":         "@o/sdk",
		"sdk-version":         "1.2.3",
		"templates":           "https://example.test/templates.json",
		"registry":            "https://registry.example.test/core",
	}
	if d := cmp.Diff(want, pins); d != "" {
		t.Errorf("pins (-want +got):\n%s", d)
	}
	if err := os.WriteFile(filepath.Join(dir, FileLock), []byte("provider: {ref: x}\n"), filePerm); err != nil {
		t.Fatal(err)
	}
	if pins, err = Flows.Pins(dir); err != nil || len(pins) != 1 {
		t.Errorf("a pin the lock lacks keeps the flag default: %v %v", pins, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileLock), []byte("provider: [\n"), filePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := Flows.Pins(dir); err == nil {
		t.Error("malformed lock: want an error")
	}
}
