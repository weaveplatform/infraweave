package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

var capturesRoot = filepath.Join(repoRoot, "test", "fixtures", "captures")

// captureEntries reads the capture records of the report 30 register.
func captureEntries(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "data", "open-questions.json"))
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

// TestAccCaptures replays every capture's calls against infraweave serve on
// a database of its own. A failure is a parity bug in infraweave, named by
// its capture.
func TestAccCaptures(t *testing.T) {
	captures, err := LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil {
		t.Fatal(err)
	}
	bin, err := binary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := DockerReachable(t.Context()); err != nil {
		if os.Getenv(envRequireDocker) == "1" {
			t.Fatalf("%s=1, and no Docker daemon answers: %v", envRequireDocker, err)
		}
		t.Skipf("the server needs PostgreSQL: %v", err)
	}
	cover, _ := coverDir(t)
	deps := &Dependencies{Migrate: func(ctx context.Context, dsn string) error {
		return Migrate(ctx, bin, cover, dsn)
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
	config, err := serverConfig(db.DSN, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := StartServer(t.Context(), bin, t.TempDir(), cover, "all", config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Stop(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	api, err := sdk.New("http://"+srv.API, sdk.WithRetry(sdk.NoRetry))
	if err != nil {
		t.Fatal(err)
	}
	real := 0
	for _, c := range captures {
		if !c.Meta.Example {
			real++
		}
		t.Run(c.ID, func(t *testing.T) {
			for i, call := range c.Calls {
				if err := Replay(t.Context(), api, call); err != nil {
					t.Errorf("%s call %d: %v", c.ID, i+1, err)
				}
			}
		})
	}
	t.Logf("replayed %d captures and the example", real)
}

// TestUnitCaptureRedaction holds the checked-in fixtures, and planted leaks
// of each kind, to the redaction rules. The planted values are assembled
// here rather than committed, so no credential-shaped string sits in the
// repository for a secret scanner to report.
func TestUnitCaptureRedaction(t *testing.T) {
	found, err := Leaks(capturesRoot)
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
			found, err := Leaks(dir)
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
	captures, err := LoadCaptures(capturesRoot, captureEntries(t))
	if err != nil || len(captures) == 0 || captures[0].ID != ExampleCapture {
		t.Fatalf("LoadCaptures = %v, %v", captures, err)
	}
	example, err := os.ReadDir(filepath.Join(capturesRoot, ExampleCapture))
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(capturesRoot, ExampleCapture, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	meta, expect := read(captureMeta), read(captureExpect)
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
		{"unknown meta key", "C0", map[string]string{captureMeta: meta + "notes: hello\n"}, "field notes not found"},
		{"unknown expect key", "C0", map[string]string{captureExpect: expect + "extra: 1\n"}, "field extra not found"},
		{"entry not the directory", "C0", map[string]string{captureMeta: asReal}, "is not the directory's name"},
		{"real capture marked example", "C7", map[string]string{captureMeta: strings.Replace(meta, "entry: C0", "entry: C7", 1)}, "only C0 is the example"},
		{"entry not in the register", "C99999", map[string]string{captureMeta: strings.Replace(asReal, "C7", "C99999", 1)}, "no capture record"},
		{"directory name", "capture-1", nil, "not named C<n>"},
		{"bad date", "C0", map[string]string{captureMeta: strings.Replace(meta, "2026-11-05", "05/11/2026", 1)}, "captured"},
		{"bad hash", "C0", map[string]string{captureMeta: strings.Replace(meta, "sha256:1fc7", "sha1:1fc7", 1)}, "introspection"},
		{"no calls", "C0", map[string]string{captureExpect: "calls: []\n"}, "no calls"},
		{"bad method", "C0", map[string]string{captureExpect: strings.Replace(expect, "method: GET", "method: FETCH", 1)}, "method"},
		{"relative path", "C0", map[string]string{captureExpect: strings.Replace(expect, "path: /api", "path: api", 1)}, "does not start with /"},
		{"bad status", "C0", map[string]string{captureExpect: strings.Replace(expect, "status: 501", "status: 999", 1)}, "status 999"},
		{"unregistered problem", "C0", map[string]string{captureExpect: strings.Replace(expect, "problem: not-implemented", "problem: teapot", 1)}, "teapot"},
		{"not a pointer", "C0", map[string]string{captureExpect: strings.Replace(expect, "/title:", "title:", 1)}, "JSON Pointer"},
		{"response not JSON", "C0", map[string]string{captureResponse: "{"}, "not JSON"},
		{"request missing", "C0", map[string]string{captureRequest: ""}, captureRequest},
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
			_, err := LoadCaptures(filepath.Dir(dir), map[string]bool{"C7": true})
			if !errors.Is(err, ErrCapture) || !strings.Contains(err.Error(), tc.want) {
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
	call := CaptureCall{
		Method: http.MethodPost, Path: "/api/v1/things",
		Headers: map[string]string{"X-Test": "yes"},
		Body:    map[string]any{"name": "n1"},
		Expect: CaptureExpect{Status: 409, Problem: "already-exists", Body: map[string]any{
			"/status": 409, "/echo": "n1", "/header": "yes",
			"/items/0/a~1b": "slash", "/items/0/c~0d": "tilde",
		}},
	}
	if err := Replay(t.Context(), api, call); err != nil {
		t.Fatalf("Replay = %v", err)
	}
	for name, mutate := range map[string]func(c *CaptureCall){
		"status":  func(c *CaptureCall) { c.Expect.Status = 501 },
		"problem": func(c *CaptureCall) { c.Expect.Problem = "not-found" },
		"no problem": func(c *CaptureCall) {
			*c = CaptureCall{Method: http.MethodGet, Path: "/api/v1/text", Expect: CaptureExpect{Status: 200, Problem: "not-found"}}
		},
		"body value":     func(c *CaptureCall) { c.Expect.Body = map[string]any{"/status": 500} },
		"missing field":  func(c *CaptureCall) { c.Expect.Body = map[string]any{"/nope": 1} },
		"index range":    func(c *CaptureCall) { c.Expect.Body = map[string]any{"/items/5": 1} },
		"index not int":  func(c *CaptureCall) { c.Expect.Body = map[string]any{"/items/x": 1} },
		"through scalar": func(c *CaptureCall) { c.Expect.Body = map[string]any{"/status/x": 1} },
		"unencodable":    func(c *CaptureCall) { c.Expect.Body = map[string]any{"/status": func() {}} },
		"not JSON answer": func(c *CaptureCall) {
			*c = CaptureCall{Method: http.MethodGet, Path: "/api/v1/text", Expect: CaptureExpect{
				Status: 200, Body: map[string]any{"/a": 1},
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := call
			mutate(&c)
			if err := Replay(t.Context(), api, c); !errors.Is(err, ErrParity) {
				t.Errorf("Replay = %v, want a parity error", err)
			}
		})
	}
	if err := Replay(t.Context(), api, CaptureCall{
		Method: http.MethodGet, Path: "/api/v1/text", Expect: CaptureExpect{Status: 200},
	}); err != nil {
		t.Errorf("a status-only expectation on a non-JSON answer: %v", err)
	}
	if err := Replay(
		t.Context(),
		client("http://127.0.0.1:0"),
		call,
	); err == nil ||
		errors.Is(err, ErrParity) {
		t.Errorf("an unreachable server: %v", err)
	}
}
