package ctl

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/private"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// jwt is a token whose claims decode, as the fake server issues it.
var jwt = "e30." + base64.RawURLEncoding.EncodeToString(
	[]byte(`{"sub":"operator:ops","iw_kind":"operator"}`)) + ".sig"

// seen is one request the fake server answered.
type seen struct {
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Auth        string          `json:"auth"`
	Idempotency string          `json:"idempotency"`
	Header      string          `json:"header"`
	Body        json.RawMessage `json:"body,omitempty"`
}

// server models the API: it exchanges the key "k1" with secret "s1" for
// jwt, answers a two-page tier and organization list, refuses to delete
// Enterprise, and echoes every other request back as it saw it.
type server struct {
	*httptest.Server

	mu   sync.Mutex
	reqs []seen
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{}
	r := chi.NewRouter()
	r.Post(sdk.ExchangePath, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID != "k1" || in.Secret != "s1" {
			problem(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jwt": jwt, "validUntil": time.Now().Add(time.Hour).Unix(),
		})
	})
	pages := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"items":[{"name":"one"}],"nextCursor":"c/1"}`)
			return
		}
		_, _ = io.WriteString(w, `{"items":[{"name":"two"}]}`)
	}
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+jwt {
					problem(w, http.StatusUnauthorized, "unauthenticated")
					return
				}
				next.ServeHTTP(w, r)
			})
		})
		r.Get(sdk.OperatorPrefix+"/tiers", pages)
		r.Get(sdk.OperatorPrefix+"/organizations", pages)
		r.Delete(
			sdk.OperatorPrefix+"/tiers/Enterprise",
			func(w http.ResponseWriter, _ *http.Request) {
				problem(w, http.StatusConflict, "invalid-state")
			},
		)
		r.Get("/api/v1/plain", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "plain text")
		})
		r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			req := seen{
				Method: r.Method,
				Path:   r.URL.EscapedPath(),
				Auth: r.Header.Get(
					"Authorization",
				),
				Idempotency: r.Header.Get(sdk.IdempotencyKeyHeader),
				Header:      r.Header.Get("X-Test"),
			}
			if len(b) > 0 {
				req.Body = b
			}
			s.mu.Lock()
			s.reqs = append(s.reqs, req)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(req)
		})
	})
	s.Server = httptest.NewServer(r)
	t.Cleanup(s.Close)
	return s
}

func problem(w http.ResponseWriter, status int, slug string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sdk.Problem{
		Type: sdk.TypePrefix + slug, Title: slug, Status: status,
	})
}

// last is the request the server echoed last.
func (s *server) last(t *testing.T) seen {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		t.Fatal("the server saw no request")
	}
	return s.reqs[len(s.reqs)-1]
}

// cli runs infraweavectl with a profiles file of its own and none of its
// variables set.
type cli struct {
	t      *testing.T
	config string
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	for _, env := range settingEnv {
		t.Setenv(env, "")
	}
	return &cli{t: t, config: filepath.Join(t.TempDir(), "conf", "config.yaml")}
}

// run runs args with stdin as standard input and returns standard output.
func (c *cli) run(stdin string, args ...string) (string, error) {
	c.t.Helper()
	root := Root()
	var out bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"--config", c.config}, args...))
	err := root.ExecuteContext(c.t.Context())
	return out.String(), err
}

// must runs args and fails the test on an error.
func (c *cli) must(stdin string, args ...string) string {
	c.t.Helper()
	out, err := c.run(stdin, args...)
	if err != nil {
		c.t.Fatalf("%v: %v", args, err)
	}
	return out
}

// operator is a CLI whose current profile holds the server's operator key.
func operator(t *testing.T) (*cli, *server) {
	t.Helper()
	s := newServer(t)
	c := newCLI(t)
	c.must("s1\n", "profile", "set", "ops", "--endpoint", s.URL, "--api-key", "k1")
	return c, s
}

func TestUnitProfiles(t *testing.T) {
	c := newCLI(t)
	if out := c.must("", "profile", "list"); out != "" {
		t.Errorf("list with no file = %q", out)
	}
	c.must(
		"s1\n",
		"profile",
		"set",
		"prod",
		"--endpoint",
		"https://iw.example.com",
		"--api-key",
		"k1",
	)
	c.must("tok\n", "profile", "set", "dev", "--endpoint", "http://localhost:8080/", "--token")
	if out := c.must("", "profile", "list"); out !=
		"  dev\thttp://localhost:8080/\n* prod\thttps://iw.example.com\n" {
		t.Errorf("list = %q", out)
	}
	if err := private.Check(c.config); err != nil {
		t.Error(err)
	}
	f, err := readFile(c.config)
	if err != nil || f.Current != "prod" ||
		f.Profiles["prod"] != (Profile{Endpoint: "https://iw.example.com", Key: "k1", Secret: "s1"}) ||
		f.Profiles["dev"] != (Profile{Endpoint: "http://localhost:8080/", Token: "tok"}) {
		t.Errorf("file %+v, %v", f, err)
	}

	c.must("", "profile", "use", "dev")
	c.must("", "profile", "delete", "dev")
	if f, _ := readFile(c.config); f.Current != "" || len(f.Profiles) != 1 {
		t.Errorf("after deleting the current profile: %+v", f)
	}
	for name, args := range map[string][]string{
		"use an unknown":    {"profile", "use", "nope"},
		"delete an unknown": {"profile", "delete", "nope"},
	} {
		if _, err := c.run("", args...); !errors.Is(err, ErrUnknownProfile) {
			t.Errorf("%s profile = %v", name, err)
		}
	}
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"an upper-case name":     {"", []string{"Prod", "--endpoint", "https://x"}},
		"a path in the endpoint": {"", []string{"p", "--endpoint", "https://x/api/v1"}},
		"no endpoint":            {"", []string{"p"}},
		"two credentials":        {"s", []string{"p", "--endpoint", "https://x", "--api-key", "k", "--token"}},
		"no secret":              {" \n", []string{"p", "--endpoint", "https://x", "--api-key", "k"}},
		"no token":               {"", []string{"p", "--endpoint", "https://x", "--token"}},
	} {
		if _, err := c.run(
			tc.stdin,
			append([]string{"profile", "set"}, tc.args...)...); !errors.Is(
			err,
			ErrInput,
		) {
			t.Errorf("profile set with %s = %v", name, err)
		}
	}
}

func TestUnitProfilesFile(t *testing.T) {
	c := newCLI(t)
	if err := os.MkdirAll(filepath.Dir(c.config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		c.config,
		[]byte("current: a\nprofiles:\n  a:\n    endpoint: https://x\n"+
			"    password: p\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run(
		"",
		"profile",
		"list",
	); err == nil ||
		!strings.Contains(err.Error(), "password") {
		t.Errorf("a key the file does not know = %v", err)
	}
	if err := os.WriteFile(c.config, []byte("profiles: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run("", "whoami"); err == nil {
		t.Error("a file that is not YAML read")
	}
	// A directory where the file should be cannot be replaced.
	if err := os.Remove(c.config); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(c.config, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run("", "profile", "set", "p", "--endpoint", "https://x"); err == nil {
		t.Error("a profile set over a directory")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c.config = filepath.Join(blocked, "config.yaml")
	if _, err := c.run("", "profile", "set", "p", "--endpoint", "https://x"); err == nil {
		t.Error("a profile set below a file")
	}
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", "")
	root := Root()
	root.SetArgs([]string{"profile", "list"})
	if err := root.ExecuteContext(t.Context()); err == nil {
		t.Error("profile list with no user configuration directory")
	}
	root = Root()
	root.SetArgs([]string{"profile", "set", "p", "--endpoint", "https://x"})
	if err := root.ExecuteContext(t.Context()); err == nil {
		t.Error("profile set with no user configuration directory")
	}
}

func TestUnitTarget(t *testing.T) {
	s := newServer(t)
	c := newCLI(t)
	if _, err := c.run("", "api", "GET", "/api/v1/x"); !errors.Is(err, ErrNoEndpoint) {
		t.Errorf("no endpoint = %v", err)
	}
	if _, err := c.run(
		"",
		"--endpoint",
		"ftp://x",
		"api",
		"GET",
		"/api/v1/x",
	); !errors.Is(
		err,
		ErrInput,
	) {
		t.Errorf("an ftp endpoint = %v", err)
	}
	if _, err := c.run(
		"",
		"--profile",
		"nope",
		"api",
		"GET",
		"/x",
	); !errors.Is(
		err,
		ErrUnknownProfile,
	) {
		t.Errorf("an unknown profile = %v", err)
	}
	c.must("wrong\n", "profile", "set", "p", "--endpoint", "http://127.0.0.1:1", "--api-key", "k1")

	// The variables replace the profile's endpoint and its whole credential.
	t.Setenv("INFRAWEAVECTL_ENDPOINT", s.URL)
	t.Setenv("INFRAWEAVECTL_API_KEY", "k1")
	t.Setenv("INFRAWEAVECTL_API_KEY_SECRET", "s1")
	c.must("", "api", "GET", "/api/v1/x")
	if got := s.last(t).Auth; got != "Bearer "+jwt {
		t.Errorf("Authorization %q", got)
	}
	t.Setenv("INFRAWEAVECTL_API_KEY", "")
	t.Setenv("INFRAWEAVECTL_TOKEN", "other")
	if _, err := c.run("", "api", "GET", "/api/v1/x"); !errors.As(err, &sdk.Unauthenticated{}) {
		t.Errorf("a token from the environment = %v", err)
	}
	// A flag outranks its variable.
	t.Setenv("INFRAWEAVECTL_TOKEN", jwt)
	t.Setenv("INFRAWEAVECTL_ENDPOINT", "http://127.0.0.1:1")
	c.must("", "--endpoint", s.URL, "api", "GET", "/api/v1/x")
}

func TestUnitWhoami(t *testing.T) {
	c, s := operator(t)
	if out := c.must(
		"",
		"whoami",
	); out != "{\n  \"sub\": \"operator:ops\",\n  \"iw_kind\": \"operator\"\n}\n" {
		t.Errorf("whoami = %q", out)
	}
	t.Setenv("INFRAWEAVECTL_API_KEY", "k1")
	t.Setenv("INFRAWEAVECTL_API_KEY_SECRET", "wrong")
	if _, err := c.run("", "whoami"); !errors.As(err, &sdk.Unauthenticated{}) {
		t.Errorf("a wrong secret = %v", err)
	}
	t.Setenv("INFRAWEAVECTL_API_KEY", "")
	for token, want := range map[string]error{"a.b": ErrInput, "a.!!.c": ErrInput, "a.e30.c": nil} {
		t.Setenv("INFRAWEAVECTL_TOKEN", token)
		if _, err := c.run("", "whoami"); !errors.Is(err, want) {
			t.Errorf("whoami with %q = %v, want %v", token, err, want)
		}
	}
	t.Setenv("INFRAWEAVECTL_TOKEN", "")
	c.must("", "profile", "set", "bare", "--endpoint", s.URL)
	if _, err := c.run("", "--profile", "bare", "whoami"); !errors.Is(err, ErrNoCredential) {
		t.Errorf("no credential = %v", err)
	}
	if _, err := c.run("", "--endpoint", "http://127.0.0.1:1", "whoami"); err == nil {
		t.Error("an exchange with no server answered")
	}
	// A server that answers the exchange with no token.
	none := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(none.Close)
	if _, err := c.run("", "--endpoint", none.URL, "whoami"); !errors.Is(err, sdk.ErrNoToken) {
		t.Errorf("an exchange with no token = %v", err)
	}
}

func TestUnitVersion(t *testing.T) {
	if out := newCLI(
		t,
	).must("", "version"); out != "infraweavectl devel (commit unknown, built unknown)\n" {
		t.Errorf("version = %q", out)
	}
	if orUnknown("x") != "x" {
		t.Error("orUnknown replaced a value")
	}
}

func TestUnitAPI(t *testing.T) {
	c, s := operator(t)
	c.must("", "api", "get", "/api/v1/stacks", "-H", "X-Test: yes")
	if r := s.last(
		t,
	); r.Method != http.MethodGet || r.Path != "/api/v1/stacks" ||
		r.Header != "yes" ||
		r.Body != nil {
		t.Errorf("GET %+v", r)
	}
	c.must("", "api", "POST", "/api/v1/stacks", "--data", `{"a":1}`)
	if r := s.last(t); string(r.Body) != `{"a":1}` || r.Idempotency == "" {
		t.Errorf("POST %+v", r)
	}
	file := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(file, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.must("", "api", "PUT", "/api/v1/x", "--data", "@"+file)
	if r := s.last(t); string(r.Body) != `{"from":"file"}` {
		t.Errorf("--data @file %s", r.Body)
	}
	c.must(`{"from":"stdin"}`, "api", "PATCH", "/api/v1/x", "--data", "-")
	if r := s.last(t); string(r.Body) != `{"from":"stdin"}` {
		t.Errorf("--data - %s", r.Body)
	}
	if out := c.must("", "api", "GET", "/api/v1/plain"); out != "plain text\n" {
		t.Errorf("a body that is not JSON printed %q", out)
	}
	for name, args := range map[string][]string{
		"--data that is not JSON": {"--data", "{"},
		"a file that is missing":  {"--data", "@" + filepath.Join(t.TempDir(), "none")},
		"a header with no colon":  {"-H", "X-Test"},
	} {
		if _, err := c.run(
			"",
			append([]string{"api", "POST", "/api/v1/x"}, args...)...); err == nil {
			t.Errorf("%s sent", name)
		}
	}
	t.Setenv("INFRAWEAVECTL_API_KEY", "k1")
	t.Setenv("INFRAWEAVECTL_API_KEY_SECRET", "wrong")
	out, err := c.run("", "operator", "billing", "get")
	if !errors.As(err, &sdk.Unauthenticated{}) || !strings.Contains(out, `"status": 401`) {
		t.Errorf("a refused exchange = %q, %v", out, err)
	}
	t.Setenv("INFRAWEAVECTL_API_KEY", "")
	out, err = c.run("", "--endpoint", "http://127.0.0.1:1", "api", "GET", "/x")
	if err == nil || out != "" {
		t.Errorf("a server that does not answer = %q, %v", out, err)
	}
	out, err = c.run("", "api", "DELETE", sdk.OperatorPrefix+"/tiers/Enterprise")
	if !errors.As(err, &sdk.InvalidState{}) || !strings.Contains(out, `"status": 409`) {
		t.Errorf("a problem = %q, %v", out, err)
	}
}

func TestUnitOperator(t *testing.T) {
	c, s := operator(t)
	for _, tc := range []struct {
		stdin              string
		args               []string
		method, path, body string
	}{
		{"", []string{"tier", "get", "Free plus"}, "GET", "/tiers/Free%20plus", ""},
		{
			"",
			[]string{"tier", "create", "Gold", "--feature", "stacks", "--feature", "scans", "--limit", "seats=4"},
			"POST", "/tiers", `{"features":["stacks","scans"],"limits":{"seats":4},"name":"Gold"}`,
		},
		{"", []string{"tier", "update", "Gold"}, "PUT", "/tiers/Gold", `{"features":[],"limits":{}}`},
		{"", []string{"tier", "delete", "Gold"}, "DELETE", "/tiers/Gold", ""},
		{"", []string{"organization", "get", "acme"}, "GET", "/organizations/acme", ""},
		{
			"",
			[]string{
				"organization", "create", "acme", "--name", "Acme", "--account-slug", "acme-dev",
				"--account-name", "Dev",
			},
			"POST", "/organizations",
			`{"account":{"name":"Dev","slug":"acme-dev"},"name":"Acme","slug":"acme"}`,
		},
		{
			"$pbkdf2\n",
			[]string{
				"organization", "create", "acme", "--name", "Acme", "--account-slug", "a",
				"--account-name", "A", "--break-glass-login", "root",
			},
			"POST", "/organizations",
			`{"account":{"name":"A","slug":"a"},"breakGlass":{"login":"root","passwordHash":"$pbkdf2"},` +
				`"name":"Acme","slug":"acme"}`,
		},
		{"", []string{"organization", "delete", "acme"}, "DELETE", "/organizations/acme", ""},
		{
			"$pbkdf2\n",
			[]string{"organization", "break-glass", "set", "acme", "--login", "root"},
			"PUT", "/organizations/acme/break-glass", `{"login":"root","passwordHash":"$pbkdf2"}`,
		},
		{
			"",
			[]string{"organization", "break-glass", "delete", "acme"},
			"DELETE", "/organizations/acme/break-glass", "",
		},
		{
			"",
			[]string{"organization", "tier", "set", "acme", "Free"},
			"PUT", "/organizations/acme/tier", `{"tier":"Free"}`,
		},
		{"", []string{"grant", "list", "acme"}, "GET", "/organizations/acme/entitlement", ""},
		{
			"",
			[]string{"grant", "feature", "set", "acme", "policies"},
			"PUT", "/organizations/acme/grants/features/policies", "",
		},
		{
			"",
			[]string{"grant", "feature", "remove", "acme", "policies"},
			"DELETE", "/organizations/acme/grants/features/policies", "",
		},
		{
			"",
			[]string{"grant", "limit", "set", "acme", "seats", "9"},
			"PUT", "/organizations/acme/grants/limits/seats", `{"value":9}`,
		},
		{
			"",
			[]string{"grant", "limit", "set", "acme", "seats", "unlimited"},
			"PUT", "/organizations/acme/grants/limits/seats", `{"value":null}`,
		},
		{
			"",
			[]string{"grant", "limit", "remove", "acme", "seats"},
			"DELETE", "/organizations/acme/grants/limits/seats", "",
		},
		{"", []string{"billing", "get"}, "GET", "/billing", ""},
		{"", []string{"billing", "set", "--enabled"}, "PUT", "/billing", `{"enabled":true}`},
		{
			"",
			[]string{"billing", "set", "--enabled=false", "--default-tier", "Free"},
			"PUT", "/billing", `{"defaultTier":"Free","enabled":false}`,
		},
	} {
		c.must(tc.stdin, append([]string{"operator"}, tc.args...)...)
		r := s.last(t)
		if r.Method != tc.method || r.Path != sdk.OperatorPrefix+tc.path ||
			string(r.Body) != tc.body {
			t.Errorf("%v sent %s %s %s; want %s %s %s", tc.args, r.Method, r.Path, r.Body,
				tc.method, tc.path, tc.body)
		}
	}
	for _, what := range []string{"tier", "organization"} {
		if out := c.must("", "operator", what, "list"); out !=
			"[\n  {\n    \"name\": \"one\"\n  },\n  {\n    \"name\": \"two\"\n  }\n]\n" {
			t.Errorf("%s list = %q", what, out)
		}
	}
	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"a limit that is no number": {"", []string{"grant", "limit", "set", "acme", "seats", "many"}},
		"no hash":                   {"", []string{"organization", "break-glass", "set", "acme", "--login", "r"}},
		"no hash on create": {"", []string{
			"organization", "create", "a", "--name", "A",
			"--account-slug", "a", "--account-name", "A", "--break-glass-login", "r",
		}},
	} {
		if _, err := c.run(
			tc.stdin,
			append([]string{"operator"}, tc.args...)...); !errors.Is(
			err,
			ErrInput,
		) {
			t.Errorf("%s = %v", name, err)
		}
	}
	t.Setenv("INFRAWEAVECTL_PROFILE", "nope")
	for _, args := range [][]string{{"tier", "list"}, {"tier", "get", "Free"}} {
		if _, err := c.run(
			"",
			append([]string{"operator"}, args...)...); !errors.Is(
			err,
			ErrUnknownProfile,
		) {
			t.Errorf("%v with an unknown profile = %v", args, err)
		}
	}
}

func TestUnitList(t *testing.T) {
	c, _ := operator(t)
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case sdk.ExchangePath:
			_ = json.NewEncoder(w).
				Encode(map[string]any{"jwt": jwt, "validUntil": time.Now().Add(time.Hour).Unix()})
		case sdk.OperatorPrefix + "/tiers":
			_, _ = io.WriteString(w, "not json")
		default:
			problem(w, http.StatusServiceUnavailable, "unavailable")
		}
	}))
	t.Cleanup(broken.Close)
	if _, err := c.run("", "--endpoint", broken.URL, "operator", "tier", "list"); err == nil {
		t.Error("a page that is not JSON listed")
	}
	out, err := c.run("", "--endpoint", broken.URL, "operator", "organization", "list")
	if err == nil || !strings.Contains(out, `"status": 503`) {
		t.Errorf("a list the server refused = %q, %v", out, err)
	}
}

func TestUnitShow(t *testing.T) {
	if err := show(failing{}, []byte("{}")); err == nil {
		t.Error("show wrote to a writer that fails")
	}
	var b bytes.Buffer
	if err := show(&b, nil); err != nil || b.Len() != 0 {
		t.Errorf("an empty body printed %q, %v", b.String(), err)
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
