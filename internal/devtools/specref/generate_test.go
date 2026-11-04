package specref

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

// writeFixtureDir lays the fixture sources out as a reference directory.
func writeFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := fixtureSources(t)
	intro, err := MarshalIntrospection(src.Schema)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{FileIntrospection: intro, FileMapping: []byte(fixtureMapping)}
	for name, v := range map[string]any{FileBruno: src.Bruno, FileTerraformSchema: src.Terraform, FileTerraformOps: src.TerraformOps} {
		if files[name], err = marshalJSON(v); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := yaml.Marshal(src.Lock)
	if err != nil {
		t.Fatal(err)
	}
	files[FileLock] = lock
	if err := writeFiles(dir, files); err != nil {
		t.Fatal(err)
	}
	return dir
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, filePerm); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf(
			"%s differs from golden; run go test ./internal/specref -update and review the diff",
			name,
		)
	}
}

func TestUnitGenerateGolden(t *testing.T) {
	dir := writeFixtureDir(t)
	g, err := GenerateDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, FileSpec, g.Spec)
	golden(t, FileCoverage, g.Coverage)
	if err := CheckDir(context.Background(), dir); err != nil {
		t.Errorf("check right after generate: %v", err)
	}
}

// TestUnitGenerateSemantics asserts the properties the golden file encodes,
// so a reviewer of an -update diff knows what must still hold.
func TestUnitGenerateSemantics(t *testing.T) {
	g, err := Generate(context.Background(), fixtureSources(t))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(g.Spec)
	if err != nil {
		t.Fatal(err)
	}
	schemas := doc.Components.Schemas

	// Embedded resources become references; GraphQL would recurse.
	if ref := schemas["Run"].Value.Properties["stack"].Ref; ref != "#/components/schemas/StackRef" {
		t.Errorf("Run.stack = %q, want StackRef", ref)
	}
	if _, ok := schemas["StackRef"].Value.Properties["id"]; !ok {
		t.Error("StackRef lacks id")
	}
	// Fields with arguments are routes, not properties.
	stack := schemas["Stack"].Value
	if _, ok := stack.Properties["runs"]; ok {
		t.Error("Stack.runs takes arguments and should be a route")
	}
	if omitted := stack.Extensions["x-graphql-omitted"].(map[string]any); omitted["runs"] != "served by GET /stacks/{stackId}/runs" {
		t.Errorf("x-graphql-omitted = %v", omitted)
	}
	// Unions carry a kind discriminator on every member.
	owner := schemas["Owner"].Value
	if owner.Discriminator == nil || owner.Discriminator.PropertyName != Discriminator ||
		len(owner.OneOf) != 2 {
		t.Errorf("Owner = %+v", owner)
	}
	if k := schemas["User"].Value.Properties[Discriminator]; k == nil || k.Value.Enum[0] != "User" {
		t.Error("User lacks kind discriminator")
	}
	// Terraform attributes annotate the inputs they set.
	space := schemas["StackInput"].Value.Properties["space"].Value
	if space.Extensions["x-terraform"] == nil {
		t.Error("StackInput.space lacks x-terraform")
	}
	if wp := schemas["StackInput"].Value.Properties["workerPool"].Value; wp.Description != "Worker pool ID." {
		t.Errorf("introspection description should win, got %q", wp.Description)
	}
	if ms := schemas["StackCreateRequest"].Value.Properties["manageState"].Value; ms.Description != "Manage state." {
		t.Errorf("Terraform description should fill a blank one, got %q", ms.Description)
	}
	// Defaults come across from GraphQL literals, enums as strings.
	if d := schemas["StackInput"].Value.Properties["state"].Value; d.Default != "READY" {
		t.Errorf("state default = %v", d.Default)
	}
	// Operations: create is 201, item routes 404, public route has no security.
	create := doc.Paths.Value("/stacks").Post
	if create.Responses.Value("201") == nil || create.Summary != "Create Stack" ||
		create.Tags[0] != "Stacks" {
		t.Errorf("stackCreate = %+v", create)
	}
	if doc.Paths.Value("/stacks/{stackId}").Get.Responses.Value("404") == nil {
		t.Error("item route lacks 404")
	}
	if sec := doc.Paths.Value("/auth/logout").Post.Security; sec == nil || len(*sec) != 0 {
		t.Error("public override should clear security")
	}
	// Bruno: Advanced sub-folder names the tag; Danger Zone defers to the resource.
	if tag := doc.Paths.Value("/oidc").Post.Tags[0]; tag != "SSO" {
		t.Errorf("oidcCreate tag = %s", tag)
	}
	if tag := doc.Paths.Value("/stacks/{stackId}").Delete.Tags[0]; tag != "Stacks" {
		t.Errorf("stackDelete tag = %s", tag)
	}
	// An example GraphQL coerces but JSON Schema rejects is dropped and reported.
	if ex := doc.Paths.Value(
		"/stacks/{stackId}/scheduled-runs",
	).Post.RequestBody.Value.Content.Get(
		jsonMedia,
	).Example; ex != nil {
		t.Errorf("invalid example kept: %v", ex)
	}
	if len(g.Report.DroppedExamples) != 1 ||
		g.Report.DroppedExamples[0] != "stackScheduledRunCreate" {
		t.Errorf("dropped = %v", g.Report.DroppedExamples)
	}
	// Structured read arguments travel JSON-encoded in the query string.
	for _, p := range doc.Paths.Value("/terraform-versions").Get.Parameters {
		if p.Value.Name == "filter" && p.Value.Content.Get(jsonMedia) == nil {
			t.Error("filter should be a JSON-encoded query parameter")
		}
	}
	if !strings.Contains(string(g.Coverage), "`enable_thing`") {
		t.Error("stale rename should show as unmapped")
	}

	// An id inside an input moves to the path: the body drops it, and the
	// inputs on the way to it become required.
	groupPut := doc.Paths.Value("/groups/{groupId}").Put
	body := groupPut.RequestBody.Value.Content.Get(jsonMedia)
	if body.Schema.Ref != "#/components/schemas/GroupUpdateInputBody" {
		t.Errorf("groupUpdate body = %q", body.Schema.Ref)
	}
	if _, ok := schemas["GroupUpdateInputBody"].Value.Properties["id"]; ok {
		t.Error("GroupUpdateInputBody keeps id")
	}
	if diff := cmp.Diff(map[string]any{"name": "ops"}, body.Example); diff != "" {
		t.Errorf("groupUpdate example (-want +got):\n%s", diff)
	}
	if d := groupPut.Parameters[0].Value.Description; d != "The group to update." {
		t.Errorf("groupId description = %q", d)
	}
	repoBody := doc.Paths.Value("/repos/{repoId}").Put.RequestBody.Value.Content.Get(jsonMedia)
	repoReq := schemas["RepoUpdateRequest"].Value
	if repoReq.Properties["custom"].Ref != "#/components/schemas/CustomUpdateInputBody" ||
		!slices.Contains(repoReq.Required, "custom") {
		t.Errorf("RepoUpdateRequest = %+v", repoReq)
	}
	wantRepo := map[string]any{"token": "t", "custom": map[string]any{"space": "root"}}
	if diff := cmp.Diff(wantRepo, repoBody.Example); diff != "" {
		t.Errorf("repoUpdate example (-want +got):\n%s", diff)
	}
	mirror := schemas["MirrorUpdateInputBody"].Value
	if mirror.Properties["custom"].Ref != "#/components/schemas/CustomUpdateInputBody" ||
		!slices.Contains(mirror.Required, "custom") {
		t.Errorf("MirrorUpdateInputBody = %+v", mirror)
	}
	// A body left with no required field carries no empty required list.
	if req := schemas["CustomUpdateInputBody"].Value.Required; !slices.Equal(
		req,
		[]string{"space"},
	) {
		t.Errorf("CustomUpdateInputBody.required = %v", req)
	}
	if schemas["LinkInputBody"].Value.Required == nil ||
		doc.Paths.Value("/repos/{repoId}/link").Put.Responses.Value("201") == nil {
		t.Error("repoLink: created override should answer 201 with LinkInputBody")
	}
}

func TestUnitCheckDirDrift(t *testing.T) {
	dir := writeFixtureDir(t)
	if err := CheckDir(context.Background(), dir); !errors.Is(err, ErrDrift) {
		t.Fatalf("missing outputs: err = %v, want ErrDrift", err)
	}
	if _, err := GenerateDir(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, FileCoverage),
		[]byte("edited\n"),
		filePerm,
	); err != nil {
		t.Fatal(err)
	}
	err := CheckDir(context.Background(), dir)
	if !errors.Is(err, ErrDrift) || !strings.Contains(err.Error(), FileCoverage) {
		t.Fatalf("err = %v, want drift in %s", err, FileCoverage)
	}
}

func TestUnitReadSourcesErrors(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string // empty means delete the file
	}{
		{"no introspection", FileIntrospection, ""},
		{"bad introspection", FileIntrospection, "{"},
		{"no mapping", FileMapping, ""},
		{"bad mapping", FileMapping, "unknown: 1"},
		{"no lock", FileLock, ""},
		{"bad lock", FileLock, "surprise: true"},
		{"no bruno", FileBruno, ""},
		{"bad bruno", FileBruno, "["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFixtureDir(t)
			path := filepath.Join(dir, tt.file)
			var err error
			if tt.content == "" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte(tt.content), filePerm)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ReadSources(dir); err == nil {
				t.Fatal("want error")
			}
			if _, err := GenerateDir(context.Background(), dir); err == nil {
				t.Fatal("GenerateDir: want error")
			}
			if err := CheckDir(context.Background(), dir); err == nil || errors.Is(err, ErrDrift) {
				t.Fatalf("CheckDir: err = %v, want a read error", err)
			}
		})
	}
	if _, err := ReadSources(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing dir: want error")
	}
	if err := CheckDir(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("CheckDir missing dir: want error")
	}
	if err := writeFiles(
		filepath.Join(t.TempDir(), "missing"),
		map[string][]byte{"x": nil},
	); err == nil {
		t.Error("writeFiles missing dir: want error")
	}
}

func TestUnitGenerateErrors(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		src := fixtureSources(t)
		src.Mapping.RPCBudget = 0
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrRPCBudget) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown scalar", func(t *testing.T) {
		src := fixtureSources(t)
		src.Schema.Type("Stack").Fields = append(
			src.Schema.Type("Stack").Fields,
			field("when", named(KindScalar, "DateTime")),
		)
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrUnknownScalar) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("discriminator clash", func(t *testing.T) {
		src := fixtureSources(t)
		src.Schema.Type("User").Fields = append(
			src.Schema.Type("User").Fields,
			field("kind", tString),
		)
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrDiscriminatorClash) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("request component clash", func(t *testing.T) {
		src := fixtureSources(t)
		src.Schema.Types = append(
			src.Schema.Types,
			&Type{
				Kind:   KindObject,
				Name:   "StackCreateRequest",
				Fields: []Field{field("x", tString)},
			},
		)
		src.Schema.Normalize()
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrComponentClash) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ref component clash", func(t *testing.T) {
		src := fixtureSources(t)
		src.Schema.Types = append(
			src.Schema.Types,
			&Type{Kind: KindObject, Name: "StackRef", Fields: []Field{field("x", tString)}},
		)
		src.Schema.Type("Run").Fields = append(
			src.Schema.Type("Run").Fields,
			field("ref", obj("StackRef")),
		)
		src.Schema.Normalize()
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrComponentClash) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("body component clash", func(t *testing.T) {
		src := fixtureSources(t)
		src.Schema.Types = append(
			src.Schema.Types,
			&Type{Kind: KindInputObject, Name: "GroupUpdateInputBody"},
		)
		src.Schema.Normalize()
		if _, err := Generate(context.Background(), src); !errors.Is(err, ErrComponentClash) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("one body dropping two fields", func(t *testing.T) {
		src := fixtureSources(t)
		src.Mapping.Operations["repoUpdate"] = OperationOverride{
			Method: "PUT",
			Path:   "/repos/by-space/{space}",
			Params: map[string]string{"space": "custom.space"},
		}
		_, err := Generate(context.Background(), src)
		if !errors.Is(err, ErrComponentClash) ||
			!strings.Contains(err.Error(), "CustomUpdateInputBody") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown scalar in arguments", func(t *testing.T) {
		for _, f := range []func(*Schema){
			func(s *Schema) { s.Type("Query").Fields[9].Args[1].Type = named(KindScalar, "Long") },                    // terraformVersions(limit)
			func(s *Schema) { s.Type("Mutation").Fields[0].Args[1].Type = nn(named(KindScalar, "Long")) },             // accountSetName? request body
			func(s *Schema) { s.Type("Query").Fields[0].Type = named(KindScalar, "Long") },                            // response
			func(s *Schema) { s.Type("StackInput").InputFields[0].Type = nn(named(KindScalar, "Long")) },              // input object
			func(s *Schema) { s.Type("SearchStacksOutput").Fields[0].Type = nn(list(nn(named(KindScalar, "Long")))) }, // list items
		} {
			src := fixtureSources(t)
			f(src.Schema)
			if _, err := Generate(context.Background(), src); !errors.Is(err, ErrUnknownScalar) {
				t.Errorf("err = %v, want ErrUnknownScalar", err)
			}
		}
	})
}

func TestUnitLockVersion(t *testing.T) {
	if v := (*Lock)(nil).version(); v != "0.0.0" {
		t.Errorf("nil lock = %s", v)
	}
	if v := fixtureLock().version(); v != "schema-abc" {
		t.Errorf("version = %s", v)
	}
	long := fixtureLock()
	long.Introspection.SHA256 = "b3255fe38f5ef8e08dfaf4fcabc47b12"
	if v := long.version(); v != "schema-b3255fe38f5e" {
		t.Errorf("version = %s", v)
	}
	if (*Lock)(nil).pins() != nil || fixtureLock().pins().Introspection.FetchedAt != "" ||
		fixtureLock().Introspection.FetchedAt == "" {
		t.Error("pins should drop the fetch time from a copy only")
	}
}

func TestUnitReport(t *testing.T) {
	r := &Report{Styles: map[string]int{StyleItem: 1}}
	md := r.Markdown()
	for _, want := range []string{"| Routed | 1 |", "in the path.\n\nNone.\n\n## Root fields", "## Root fields no Bruno request calls\n\nNone."} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	if codeList(nil) != "—" || codeList([]string{"a", "b"}) != "`a`, `b`" {
		t.Error("codeList")
	}
}
