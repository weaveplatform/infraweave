package specref

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// tarball builds a codeload-style archive: every path under one top-level
// directory, as GitHub serves it.
func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(
		&tar.Header{Name: "repo-ref/", Typeflag: tar.TypeDir, Mode: 0o755},
	); err != nil {
		t.Fatal(err)
	}
	for _, name := range sortedKeys(files) {
		hdr := &tar.Header{
			Name:     "repo-ref/" + name,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(files[name])),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnitParseIntrospection(t *testing.T) {
	good, err := MarshalIntrospection(fixtureSchema())
	if err != nil {
		t.Fatal(err)
	}
	s, err := ParseIntrospection(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Queries()) == 0 || len(s.Mutations()) == 0 || s.Type("Stack") == nil {
		t.Fatal("index not built")
	}
	again, err := MarshalIntrospection(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(good, again) {
		t.Error("marshal is not stable across a round trip")
	}
	tests := map[string]struct {
		in   string
		want error
	}{
		"not json":  {"{", nil},
		"errors":    {`{"errors":[{"message":"no"}]}`, ErrIntrospection},
		"no schema": {`{"data":{}}`, ErrNoSchema},
		"missing query": {
			`{"data":{"__schema":{"queryType":{"name":"Query"},"types":[]}}}`,
			ErrUnknownRootType,
		},
	}
	for name, tt := range tests {
		_, err := ParseIntrospection([]byte(tt.in))
		if err == nil || tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tt.want)
		}
	}
	empty := &Schema{}
	if empty.Queries() != nil || empty.Mutations() != nil {
		t.Error("schema without roots should have no root fields")
	}
}

func TestUnitTypeRef(t *testing.T) {
	ty := nn(list(nn(obj("Stack"))))
	if !ty.NonNull() || !ty.IsList() || ty.Named().Name != "Stack" ||
		ty.Elem().Kind != KindNonNull {
		t.Errorf("helpers wrong for %s", ty)
	}
	if got := ty.String(); got != "[Stack!]!" {
		t.Errorf("String = %s", got)
	}
	if plain := obj("Run"); plain.IsList() || plain.Elem().Name != "Run" {
		t.Error("non-list Elem should return itself")
	}
}

func TestUnitNames(t *testing.T) {
	words := map[string][]string{
		"githubEnterpriseIntegration": {"github", "Enterprise", "Integration"},
		"VCSIntegration":              {"VCS", "Integration"},
		"repoID":                      {"repo", "ID"},
		"stack":                       {"stack"},
		"run2Fast":                    {"run2", "Fast"},
	}
	for in, want := range words {
		if diff := cmp.Diff(want, wordsOf(in)); diff != "" {
			t.Errorf("words(%s) (-want +got):\n%s", in, diff)
		}
	}
	for in, want := range map[string]string{"VCSAgentPool": "vcs-agent-pool", "stackScheduledRun": "stack-scheduled-run", "oidc": "oidc"} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%s) = %s", in, got)
		}
	}
	irregular := map[string]string{"oidc": "oidc"}
	for in, want := range map[string]string{"stack": "stacks", "policy": "policies", "key": "keys", "class": "classes", "box": "boxes", "branch": "branches", "oidc": "oidc", "workerPool": "workerPools"} {
		if got := plural(in, irregular); got != want {
			t.Errorf("plural(%s) = %s, want %s", in, got, want)
		}
	}
	if !hasWordPrefix("stackScheduledRun", "stack") || hasWordPrefix("stacks", "stack") ||
		hasWordPrefix("stack", "stack") ||
		hasWordPrefix("run", "stack") {
		t.Error("hasWordPrefix")
	}
	for in, want := range map[string][2]string{"contextCreateV2": {"contextCreate", "V2"}, "policyCreatev2": {"policyCreate", "v2"}, "stack": {"stack", ""}, "V2": {"V2", ""}} {
		base, v := stripVersion(in)
		if base != want[0] || v != want[1] {
			t.Errorf("stripVersion(%s) = %s %s", in, base, v)
		}
	}
	if snakeToCamel("worker_pool_id") != "workerPoolId" || camel(nil) != "" ||
		lowerFirst("") != "" ||
		upperFirst("") != "" ||
		lowerFirst("VCS") != "vcs" {
		t.Error("case helpers")
	}
	if versionRank("thingV12") != 12 || versionRank("thing") != 1 {
		t.Error("versionRank")
	}
}

// wordsOf keeps the test table readable next to the words function.
func wordsOf(s string) []string { return words(s) }

const bruRequest = `meta {
  name: Create Stack
  type: graphql
  seq: 3
}

docs {
  Create a new stack.

  <!-- notes: hand-written, preserved by sync-docs -->

  Not part of the API description.
}

body:graphql {
  # leading comment
  mutation CreateStack($input: StackInput!, $flag: Boolean! = false) @live {
    created: stackCreate(input: $input, note: "a } in a string") {
      id
      ... on Stack { name }
    }
    ...Extra
    other @include(if: true)
  }
}

body:graphql:vars {
  {
    "input": {"name": "my-stack"}
  }
}
`

func TestUnitParseBrunoRequest(t *testing.T) {
	op, err := ParseBrunoRequest(
		"Advanced/SSO/Create Stack.bru",
		[]byte(strings.ReplaceAll(bruRequest, "\n", "\r\n")),
	)
	if err != nil {
		t.Fatal(err)
	}
	want := &BrunoOperation{
		Path:       "Advanced/SSO/Create Stack.bru",
		Folder:     "Advanced",
		Subfolder:  "SSO",
		Name:       "Create Stack",
		Operation:  "mutation",
		RootFields: []string{"stackCreate", "other"},
		Docs:       "Create a new stack.",
		Variables:  json.RawMessage(`{"input":{"name":"my-stack"}}`),
	}
	if diff := cmp.Diff(want, op); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}

	op, err = ParseBrunoRequest(
		"Auth/Token.bru",
		[]byte(
			"meta {\n  name: Token\n}\n\nbody:graphql {\n  { viewer { id } }\n}\n\nbody:graphql:vars {\n  {{not json}}\n}\n",
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	if op.Operation != "query" || op.RootFields[0] != "viewer" || op.Variables != nil ||
		op.Subfolder != "" {
		t.Errorf("shorthand query = %+v", op)
	}

	if op, err := ParseBrunoRequest(
		"x.bru",
		[]byte("meta {\n  name: x\n}\n"),
	); op != nil ||
		err != nil {
		t.Errorf("no body: op = %v, err = %v", op, err)
	}
	if _, err := ParseBrunoRequest(
		"x.bru",
		[]byte("meta {\n  name: x\n"),
	); !errors.Is(
		err,
		ErrBruUnterminated,
	) {
		t.Errorf("unterminated: err = %v", err)
	}
	for _, body := range []string{"query Q($a: Int)", "query { }"} {
		if _, err := ParseBrunoRequest(
			"x.bru",
			[]byte("body:graphql {\n  "+body+"\n}\n"),
		); !errors.Is(
			err,
			ErrNoOperation,
		) {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	if metaName("type: graphql") != "" {
		t.Error("metaName without name")
	}
}

func TestUnitReadBrunoTarball(t *testing.T) {
	tgz := tarball(t, map[string]string{
		"README.md":                         "ignored",
		"Spacelift/collection.bru":          "meta {\n  name: Spacelift\n}\n",
		"Spacelift/environments/my.bru":     "vars {\n}\n",
		"Spacelift/Stacks/folder.bru":       "meta {\n  name: Stacks\n}\n\ndocs {\n  Stacks.\n\n  <!-- notes -->\n  hidden\n}\n",
		"Spacelift/Stacks/Create Stack.bru": bruRequest,
		"Spacelift/Stacks/Notes.bru":        "meta {\n  name: Notes\n}\n",
		"Spacelift/Stacks/Get.bru":          "body:graphql {\n  query { stack(id: 1) { id } }\n}\n",
	})
	inv, err := ReadBrunoTarball(tgz)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Folders) != 1 || inv.Folders[0].Docs != "Stacks." ||
		inv.Folders[0].Name != "Stacks" {
		t.Errorf("folders = %+v", inv.Folders)
	}
	if len(inv.Requests) != 2 || inv.Requests[0].Path != "Stacks/Create Stack.bru" ||
		inv.Requests[1].RootFields[0] != "stack" {
		t.Errorf("requests = %+v", inv.Requests)
	}
	for name, tgz := range map[string][]byte{
		"bad folder":  tarball(t, map[string]string{"Spacelift/A/folder.bru": "meta {\n"}),
		"bad request": tarball(t, map[string]string{"Spacelift/A/x.bru": "meta {\n"}),
		"not gzip":    []byte("plain"),
		"bad tar":     gzipOf(t, []byte("not a tar stream at all, just bytes that are long enough to fail")),
	} {
		if _, err := ReadBrunoTarball(tgz); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func gzipOf(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnitWalkTarballLimits(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	// A header claiming more than the limit is refused before any read.
	if err := tw.WriteHeader(
		&tar.Header{Name: "repo/big", Typeflag: tar.TypeReg, Size: maxTarEntry + 1},
	); err != nil {
		t.Fatal(err)
	}
	_ = tw.Flush()
	_ = zw.Close()
	err := walkTarball(buf.Bytes(), func(string, []byte) error { return nil })
	if !errors.Is(err, ErrTarEntryTooLarge) {
		t.Errorf("err = %v", err)
	}
	stop := errors.New("stop")
	tgz := tarball(t, map[string]string{"a": "1", "nested/b": "2"})
	var seen []string
	err = walkTarball(tgz, func(p string, _ []byte) error {
		seen = append(seen, p)
		return stop
	})
	if !errors.Is(err, stop) || len(seen) != 1 {
		t.Errorf("callback error should stop the walk: %v %v", err, seen)
	}
	topLevel := tarballRaw(t, "toplevel-file")
	if err := walkTarball(
		topLevel,
		func(p string, _ []byte) error { return errors.New("unexpected " + p) },
	); err != nil {
		t.Errorf("a file outside the repo directory is skipped: %v", err)
	}
}

// tarballRaw writes one regular file at exactly name, with no repo directory.
func tarballRaw(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

const providerResource = "package spacelift\n\nimport \"github.com/shurcooL/graphql\"\n\n" +
	"func create() {\n\tvar mutation struct {\n" +
	"\t\tCreate struct{ ID string `graphql:\"id\"` } `graphql:\"stackCreate(input: $input)\"`\n" +
	"\t\tSpread struct{} `graphql:\"... on Stack\"`\n" +
	"\t\tPlain string `json:\"plain\"`\n" +
	"\t\tBad string `not \"quoted`\n" +
	"\t}\n\t_ = mutation\n}\n\n" +
	"func read() {\n\tvar query struct {\n\t\tStack struct{ Name string `graphql:\"name\"` } `graphql:\"stack(id: $id)\"`\n\t}\n\t_ = query\n}\n"

func TestUnitReadProviderTarball(t *testing.T) {
	tgz := tarball(t, map[string]string{
		"spacelift/resource_stack.go":      providerResource,
		"spacelift/resource_stack_test.go": "package spacelift\n",
		"spacelift/data_stack.go":          "package spacelift\n\ntype q struct {\n\tS string `graphql:\"stack(id: $id)\"`\n}\n",
		"spacelift/data_empty.go":          "package spacelift\n",
		"spacelift/provider.go":            "package spacelift\n\ntype q struct {\n\tS string `graphql:\"stack\"`\n}\n",
		"spacelift/internal/structs/x.go":  "package structs\n",
		"spacelift/README.md":              "ignored",
	})
	ops, err := ReadProviderTarball(tgz)
	if err != nil {
		t.Fatal(err)
	}
	want := &TerraformOperations{
		Resources:   map[string][]string{"spacelift_stack": {"id", "name", "stack", "stackCreate"}},
		DataSources: map[string][]string{"spacelift_stack": {"stack"}},
	}
	if diff := cmp.Diff(want, ops); diff != "" {
		t.Errorf("(-want +got):\n%s", diff)
	}
	ops.RetainRootFields(fixtureSchema())
	want.Resources["spacelift_stack"] = []string{"stack", "stackCreate"}
	if diff := cmp.Diff(want, ops); diff != "" {
		t.Errorf("after retain (-want +got):\n%s", diff)
	}
	gone := &TerraformOperations{
		Resources:   map[string][]string{"spacelift_x": {"nothing"}},
		DataSources: map[string][]string{},
	}
	gone.RetainRootFields(fixtureSchema())
	if len(gone.Resources) != 0 {
		t.Error("resource with no root fields should be dropped")
	}
	if _, err := ReadProviderTarball(
		tarball(t, map[string]string{"spacelift/resource_bad.go": "package"}),
	); err == nil {
		t.Error("unparsable source: want error")
	}
	if _, err := ReadProviderTarball([]byte("x")); err == nil {
		t.Error("bad tarball: want error")
	}
}

func TestUnitParseMapping(t *testing.T) {
	m, err := ParseMapping(nil)
	if err != nil || m.RPCBudget != 0 {
		t.Fatalf("empty mapping: %v %+v", err, m)
	}
	for name, in := range map[string]string{
		"unknown key":      "rpcBudgte: 3",
		"no method":        "operations: {x: {path: /x}}",
		"no path":          "operations: {x: {method: GET}}",
		"exclusion reason": "exclude: {x: ''}",
		"wrong type":       "rpcBudget: lots",
	} {
		if _, err := ParseMapping([]byte(in)); !errors.Is(err, ErrMappingInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
