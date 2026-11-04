package specref

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// flowsUpstream models what fetch --product flows talks to: codeload serving
// the provider and flowctl tarballs, the npm registry serving the SDK, the
// template index and an app registry. Tests replace a body to break one
// source.
type flowsUpstream struct {
	srv    *httptest.Server
	bodies map[string][]byte
}

func newFlowsUpstream(t *testing.T) *flowsUpstream {
	t.Helper()
	u := &flowsUpstream{bodies: map[string][]byte{
		"provider": flowsProviderTarball(t),
		"flowctl":  tarball(t, map[string]string{flowctlAPI: flowctlFixture}),
		"sdk":      tarball(t, map[string]string{flowsSDKDeclarations: flowsSDKFixture}),
		"templates": mustJSON(t, []flowTemplate{
			{Slug: "one", Flow: "blocks:\n  - name: A\n    config:\n      when: !expr outputs.x\n"},
			{Slug: "two", Flow: "blocks: []\n"},
		}),
		"versions": []byte(`{"versions": [{"version": "1.0.0"}]}`),
	}}
	r := chi.NewRouter()
	r.Get("/{owner}/{repo}/tar.gz/{ref}", func(w http.ResponseWriter, req *http.Request) {
		switch chi.URLParam(req, "repo") {
		case "terraform-provider-flows":
			_, _ = w.Write(u.bodies["provider"])
		case "flowctl":
			_, _ = w.Write(u.bodies["flowctl"])
		default:
			http.NotFound(w, req)
		}
	})
	r.Get("/npm/@o/sdk/-/sdk-1.2.3.tgz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(u.bodies["sdk"])
	})
	r.Get("/templates.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(u.bodies["templates"])
	})
	r.Get("/registry/core", func(w http.ResponseWriter, _ *http.Request) {
		if b, ok := u.bodies["manifest"]; ok {
			_, _ = w.Write(b)
			return
		}
		_, _ = io.WriteString(w, `{"apps": {"a": {"appMetadataUrl": "`+u.srv.URL+`/registry/a/versions.json"}}}`)
	})
	r.Get("/registry/a/versions.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(u.bodies["versions"])
	})
	u.srv = httptest.NewServer(r)
	t.Cleanup(u.srv.Close)
	return u
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// flowsFetchDir is a directory holding only what fetch reads: the overlay.
func flowsFetchDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileFlowsOverlay), []byte(flowsOverlayFixture), filePerm); err != nil {
		t.Fatal(err)
	}
	return dir
}

func flowsFetchOptions(u *flowsUpstream, dir string) FlowsFetchOptions {
	return FlowsFetchOptions{
		Dir:          dir,
		Codeload:     u.srv.URL,
		NPM:          u.srv.URL + "/npm",
		ProviderRepo: "o/terraform-provider-flows",
		ProviderRef:  "1111111111111111111111111111111111111111",
		FlowctlRepo:  "o/flowctl",
		FlowctlRef:   "2222222222222222222222222222222222222222",
		SDKPackage:   "@o/sdk",
		SDKVersion:   "1.2.3",
		Templates:    u.srv.URL + "/templates.json",
		Registry:     u.srv.URL + "/registry/core",
		Fetcher:      testFetcher(),
		Now:          func() time.Time { return time.Date(2026, 11, 4, 7, 0, 0, 0, time.UTC) },
	}
}

func TestAccFlowsFetch(t *testing.T) {
	u := newFlowsUpstream(t)
	dir := flowsFetchDir(t)
	if err := FetchFlows(context.Background(), flowsFetchOptions(u, dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileMapping), []byte(flowsMappingFixture), filePerm); err != nil {
		t.Fatal(err)
	}
	src, err := ReadFlowsSources(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := src.Lock
	if l.FetchedAt != "2026-11-04T07:00:00Z" || l.Provider.Ref != "1111111111111111111111111111111111111111" ||
		len(l.Flowctl.SHA256) != 64 || l.SDK.Version != "1.2.3" || len(l.Templates.Flows) != 2 ||
		l.Registry.Manifest != u.srv.URL+"/registry/core" {
		t.Errorf("lock = %+v", l)
	}
	if len(src.Provider.RPCs) != 11 || len(src.Flowctl.RPCs) != 8 {
		t.Errorf("rpcs: %d provider, %d flowctl", len(src.Provider.RPCs), len(src.Flowctl.RPCs))
	}
	if _, err := Flows.GenerateDir(context.Background(), dir); err != nil {
		t.Fatalf("a fresh fetch must generate: %v", err)
	}
	if err := Flows.Inventory(dir, InventoryOutputs{Register: filepath.Join(t.TempDir(), "r.json")}, false); err != nil {
		t.Errorf("a fresh fetch must inventory: %v", err)
	}
}

func TestAccFlowsFetchFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		body, content string
		mutate        func(*FlowsFetchOptions)
		want          string
	}{
		"branch ref":    {mutate: func(o *FlowsFetchOptions) { o.FlowctlRef = "main" }, want: "flowctl ref"},
		"no overlay":    {mutate: func(o *FlowsFetchOptions) { o.Dir = filepath.Join(o.Dir, "missing") }, want: "open reference dir"},
		"no provider":   {mutate: func(o *FlowsFetchOptions) { o.ProviderRepo = "o/nope" }, want: "404"},
		"bad provider":  {body: "provider", content: "nope", want: "open tarball"},
		"no flowctl":    {mutate: func(o *FlowsFetchOptions) { o.FlowctlRepo = "o/nope" }, want: "404"},
		"bad flowctl":   {body: "flowctl", content: "nope", want: "open tarball"},
		"no api.ts":     {body: "flowctl", content: "readme", want: "the tarball has no src/api.ts"},
		"flowctl parse": {body: "flowctl", content: "api", want: "unterminated string"},
		"no sdk":        {mutate: func(o *FlowsFetchOptions) { o.SDKVersion = "9.9.9" }, want: "404"},
		"bad sdk":       {body: "sdk", content: "nope", want: "open tarball"},
		"sdk parse":     {body: "sdk", content: "sdk", want: "flows client source"},
		"no templates":  {mutate: func(o *FlowsFetchOptions) { o.Templates = o.Registry + "/nope" }, want: "404"},
		"bad templates": {body: "templates", content: "{", want: "decode"},
		"no template":   {body: "templates", content: "[]", want: "lists no templates"},
		"blank":         {body: "templates", content: `[{"slug": "x"}]`, want: "has no slug or flow"},
		"bad template":  {body: "templates", content: `[{"slug": "x", "flow": "extra: 1"}]`, want: "template x: does not match schemas.yaml"},
		"no registry":   {mutate: func(o *FlowsFetchOptions) { o.Registry += "/nope" }, want: "404"},
		"bad manifest":  {body: "manifest", content: `{}`, want: "registry"},
		"manifest json": {body: "manifest", content: `[`, want: "decode"},
		"no apps":       {body: "manifest", content: `{"apps": {}}`, want: "lists no apps"},
		"elsewhere": {
			body: "manifest", content: `{"apps": {"a": {"appMetadataUrl": "https://elsewhere.test/v.json"}}}`,
			want: "is not on",
		},
		"bad registry url": {mutate: func(o *FlowsFetchOptions) { o.Registry = "http://[::1" }, want: ""},
		"no versions": {
			body: "manifest", content: `{"apps": {"a": {"appMetadataUrl": "SRV/registry/b/versions.json"}}}`,
			want: "404",
		},
		"bad versions": {body: "versions", content: `{}`, want: "app a versions"},
	} {
		t.Run(name, func(t *testing.T) {
			u := newFlowsUpstream(t)
			if tc.body != "" {
				content := strings.ReplaceAll(tc.content, "SRV", u.srv.URL)
				switch content {
				case "readme":
					u.bodies[tc.body] = tarball(t, map[string]string{"README.md": "x"})
				case "api":
					u.bodies[tc.body] = tarball(t, map[string]string{flowctlAPI: `const s = "open`})
				case "sdk":
					u.bodies[tc.body] = tarball(t, map[string]string{flowsSDKDeclarations: `"open`})
				default:
					u.bodies[tc.body] = []byte(content)
				}
			}
			dir := flowsFetchDir(t)
			o := flowsFetchOptions(u, dir)
			if tc.mutate != nil {
				tc.mutate(&o)
			}
			err := FetchFlows(context.Background(), o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("a failed fetch wrote %d files", len(entries)-1)
			}
		})
	}
}
