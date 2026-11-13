package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

var capturesRoot = filepath.Join(harness.RepoRoot, "test", "fixtures", "captures")

// captureEntries reads the capture records of the open-questions register.
//
// ref: report 30
func captureEntries(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(harness.RepoRoot, "docs", "data", "open-questions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Records []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range reg.Records {
		if r.Kind == "capture" {
			out[r.ID] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the register has no capture records")
	}
	return out
}

// awaited are the captures a feature stands in for until each is
// recorded, by the feature that asserts the choices made meanwhile.
// TestAccCaptures runs each one missing from the fixtures as a skipped
// subtest, so a run names what it still waits for.
var awaited = map[string]string{
	"C105": "state/backend.feature",
	"C111": "state/backend.feature",
}

// TestAccCaptures replays every capture's calls against infraweave serve on
// a database of its own. A failure is a parity bug in infraweave, named by
// its capture.
func TestAccCaptures(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := harness.Binary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.DockerReachable(t.Context()); err != nil {
		if os.Getenv(harness.EnvRequireDocker) == "1" {
			t.Fatalf("%s=1, and no Docker daemon answers: %v", harness.EnvRequireDocker, err)
		}
		t.Skipf("the server needs PostgreSQL: %v", err)
	}
	cover, _ := harness.CoverDir(t)
	deps := &deps.Dependencies{Migrate: func(ctx context.Context, dsn string) error {
		return harness.Migrate(ctx, bin, cover, dsn)
	}}
	t.Cleanup(func() {
		if err := deps.Close(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	db, err := deps.Postgres(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config, err := harness.ServerConfig(db.DSN, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := harness.StartServer(t.Context(), bin, t.TempDir(), cover, "all", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Stop(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	api, _, err := harness.Authenticated(t.Context(), db, "http://"+srv.API, harness.Home)
	if err != nil {
		t.Fatal(err)
	}
	real := 0
	recorded := map[string]bool{}
	for _, c := range captures {
		recorded[c.ID] = true
		if !c.Meta.Example {
			real++
		}
		t.Run(c.ID, func(t *testing.T) {
			for i, call := range c.Calls {
				if err := harness.Replay(t.Context(), api, call); err != nil {
					t.Errorf("%s call %d: %v", c.ID, i+1, err)
				}
			}
		})
	}
	for _, id := range slices.Sorted(maps.Keys(awaited)) {
		if !recorded[id] {
			t.Run(id, func(t *testing.T) {
				t.Skipf("not recorded; %s asserts the choices made until it is", awaited[id])
			})
		}
	}
	t.Logf("replayed %d captures and the example", real)
}

// TestUnitAwaitedCaptures holds each awaited capture to a capture record
// of the register and a feature that exists.
func TestUnitAwaitedCaptures(t *testing.T) {
	entries := captureEntries(t)
	for id, feature := range awaited {
		if !entries[id] {
			t.Errorf("%s is no capture record of the register", id)
		}
		if _, err := os.Stat(filepath.Join("features", feature)); err != nil {
			t.Errorf("%s awaits with %s: %v", id, feature, err)
		}
	}
}

// TestUnitCaptureRedaction holds the checked-in fixtures, and planted leaks
// of each kind, to the redaction rules. The planted values are assembled
// here rather than committed, so no credential-shaped string sits in the
// repository for a secret scanner to report.
func TestUnitCaptureRedaction(t *testing.T) {
	found, err := harness.Leaks(capturesRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("the capture fixtures leak %s", f)
	}
	jwtPart := "eyJ" + strings.Repeat("hbGciOiJSUzI1NiJ9", 2)
	for _, tc := range []struct {
		name, content, want string
	}{
		{"jwt", `{"token": "` + jwtPart + "." + jwtPart + `.c2lnbmF0dXJl"}`, "a JWT"},
		{"aws", "aws_access_key_id = AKIA" + "ABCDEFGHIJKLMNOP", "an AWS access key"},
		{"pem", "-----BEGIN RSA " + "PRIVATE KEY-----", "a private key block"},
		{"secret", `"apiKeySecret": "` + strings.Repeat("0123456789abcdef", 2) + `"`, "an API key secret"},
		{"mail", "owner: someone@" + "spacelift.io", "an e-mail address outside example.com"},
		{"clean", "owner: someone@example.com\ntoken: <redacted>\nid: AKIA-shaped but short", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(dir, "response.json"),
				[]byte(tc.content),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			found, err := harness.Leaks(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want == "" && len(found) > 0:
				t.Errorf("a clean file leaks: %q", found)
			case tc.want != "" && !containsSuffix(found, ": "+tc.want):
				t.Errorf("found %q, want %s", found, tc.want)
			}
		})
	}
}

func containsSuffix(found []string, suffix string) bool {
	for _, f := range found {
		if strings.HasSuffix(f, suffix) {
			return true
		}
	}
	return false
}

// TestUnitLoadCaptures holds a capture directory to the layout: every file
// present, both YAML files strict, and the entry a register record.
func TestUnitLoadCaptures(t *testing.T) {
	captures, err := harness.LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil || len(captures) == 0 || captures[0].ID != harness.ExampleCapture {
		t.Fatalf("LoadCaptures = %v, %v", captures, err)
	}
	example, err := os.ReadDir(filepath.Join(capturesRoot, harness.ExampleCapture))
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(capturesRoot, harness.ExampleCapture, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	meta, expect := read(harness.MetaFile), read(harness.ExpectFile)
	asReal := strings.Replace(
		strings.Replace(meta, "entry: C0", "entry: C7", 1),
		"example: true",
		"example: false",
		1,
	)
	for _, tc := range []struct {
		name, dir string
		files     map[string]string
		want      string
	}{
		{"unknown meta key", "C0", map[string]string{harness.MetaFile: meta + "notes: hello\n"}, "field notes not found"},
		{"unknown expect key", "C0", map[string]string{harness.ExpectFile: expect + "extra: 1\n"}, "field extra not found"},
		{"entry not the directory", "C0", map[string]string{harness.MetaFile: asReal}, "is not the directory's name"},
		{"real capture marked example", "C7", map[string]string{harness.MetaFile: strings.Replace(meta, "entry: C0", "entry: C7", 1)}, "only C0 is the example"},
		{"entry not in the register", "C99999", map[string]string{harness.MetaFile: strings.Replace(asReal, "C7", "C99999", 1)}, "no capture record"},
		{"directory name", "capture-1", nil, "not named C<n>"},
		{"bad date", "C0", map[string]string{harness.MetaFile: strings.Replace(meta, "2026-11-05", "05/11/2026", 1)}, "captured"},
		{"bad hash", "C0", map[string]string{harness.MetaFile: strings.Replace(meta, "sha256:1fc7", "sha1:1fc7", 1)}, "introspection"},
		{"no calls", "C0", map[string]string{harness.ExpectFile: "calls: []\n"}, "no calls"},
		{"bad method", "C0", map[string]string{harness.ExpectFile: strings.Replace(expect, "method: GET", "method: FETCH", 1)}, "method"},
		{"relative path", "C0", map[string]string{harness.ExpectFile: strings.Replace(expect, "path: /api", "path: api", 1)}, "does not start with /"},
		{"bad status", "C0", map[string]string{harness.ExpectFile: strings.Replace(expect, "status: 404", "status: 999", 1)}, "status 999"},
		{"unregistered problem", "C0", map[string]string{harness.ExpectFile: strings.Replace(expect, "problem: not-found", "problem: teapot", 1)}, "teapot"},
		{"not a pointer", "C0", map[string]string{harness.ExpectFile: strings.Replace(expect, "/title:", "title:", 1)}, "JSON Pointer"},
		{"response not JSON", "C0", map[string]string{harness.ResponseFile: "{"}, "not JSON"},
		{"request missing", "C0", map[string]string{harness.RequestFile: ""}, harness.RequestFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.dir)
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			for _, e := range example {
				content, ok := tc.files[e.Name()]
				if !ok {
					content = read(e.Name())
				} else if content == "" {
					continue
				}
				if err := os.WriteFile(
					filepath.Join(dir, e.Name()),
					[]byte(content),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			_, err := harness.LoadCaptures(filepath.Dir(dir), map[string]bool{"C7": true})
			if !errors.Is(err, harness.ErrCapture) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("LoadCaptures = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestUnitReplay checks each assertion against a server that answers a
// known problem, and that a wrong expectation is reported as a parity bug.
func TestUnitReplay(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/api/v1/things", func(w http.ResponseWriter, req *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(req.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "urn:infraweave:problem:already-exists", "status": 409,
			"echo": in["name"], "header": req.Header.Get("X-Test"),
			"items": []any{map[string]any{"a/b": "slash", "c~d": "tilde"}},
		})
	})
	r.Get(
		"/api/v1/text",
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("plain")) },
	)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	client := func(base string) *sdk.Client {
		c, err := sdk.New(base, sdk.WithRetry(sdk.NoRetry))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	api := client(srv.URL)
	call := harness.CaptureCall{
		Method: http.MethodPost, Path: "/api/v1/things",
		Headers: map[string]string{"X-Test": "yes"},
		Body:    map[string]any{"name": "n1"},
		Expect: harness.CaptureExpect{Status: 409, Problem: "already-exists", Body: map[string]any{
			"/status": 409, "/echo": "n1", "/header": "yes",
			"/items/0/a~1b": "slash", "/items/0/c~0d": "tilde",
		}},
	}
	if err := harness.Replay(t.Context(), api, call); err != nil {
		t.Fatalf("Replay = %v", err)
	}
	for name, mutate := range map[string]func(c *harness.CaptureCall){
		"status":  func(c *harness.CaptureCall) { c.Expect.Status = 501 },
		"problem": func(c *harness.CaptureCall) { c.Expect.Problem = "not-found" },
		"no problem": func(c *harness.CaptureCall) {
			*c = harness.CaptureCall{Method: http.MethodGet, Path: "/api/v1/text", Expect: harness.CaptureExpect{Status: 200, Problem: "not-found"}}
		},
		"body value":     func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/status": 500} },
		"missing field":  func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/nope": 1} },
		"index range":    func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/items/5": 1} },
		"index not int":  func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/items/x": 1} },
		"through scalar": func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/status/x": 1} },
		"unencodable":    func(c *harness.CaptureCall) { c.Expect.Body = map[string]any{"/status": func() {}} },
		"not JSON answer": func(c *harness.CaptureCall) {
			*c = harness.CaptureCall{Method: http.MethodGet, Path: "/api/v1/text", Expect: harness.CaptureExpect{
				Status: 200, Body: map[string]any{"/a": 1},
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := call
			mutate(&c)
			if err := harness.Replay(t.Context(), api, c); !errors.Is(err, harness.ErrParity) {
				t.Errorf("Replay = %v, want a parity error", err)
			}
		})
	}
	if err := harness.Replay(t.Context(), api, harness.CaptureCall{
		Method: http.MethodGet, Path: "/api/v1/text", Expect: harness.CaptureExpect{Status: 200},
	}); err != nil {
		t.Errorf("a status-only expectation on a non-JSON answer: %v", err)
	}
	if err := harness.Replay(
		t.Context(),
		client("http://127.0.0.1:0"),
		call,
	); err == nil ||
		errors.Is(err, harness.ErrParity) {
		t.Errorf("an unreachable server: %v", err)
	}
}
