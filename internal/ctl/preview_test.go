package ctl

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

func TestUnitPreview(t *testing.T) {
	c, s := operator(t)
	if out := c.must("", "preview", "show"); out !=
		"{\n  \"features\": [],\n  \"account\": {\n    \"enabled\": false,\n"+
			"    \"canManage\": true\n  },\n  \"visible\": false\n}\n" {
		t.Errorf("preview show = %q", out)
	}
	if r := s.last(t); r.Method != http.MethodGet || r.Path != previewPath {
		t.Errorf("preview show sent %s %s", r.Method, r.Path)
	}
	for _, tc := range []struct {
		args               []string
		method, path, body string
	}{
		{[]string{"account", "enable"}, "POST", previewAccountPath, `{"enabled":true}`},
		{[]string{"account", "disable"}, "POST", previewAccountPath, `{"enabled":false}`},
		{[]string{"personal", "enable"}, "PUT", previewPersonalPath, `{"enabled":true}`},
		{[]string{"personal", "disable"}, "PUT", previewPersonalPath, `{"enabled":false}`},
	} {
		out := c.must("", append([]string{"preview"}, tc.args...)...)
		r := s.last(t)
		if r.Method != tc.method || r.Path != tc.path || string(r.Body) != tc.body ||
			!strings.Contains(out, `"path": "`+tc.path+`"`) {
			t.Errorf("%v sent %s %s %s and printed %q; want %s %s %s", tc.args, r.Method, r.Path,
				r.Body, out, tc.method, tc.path, tc.body)
		}
		if (tc.method == http.MethodPost) != (r.Idempotency != "") {
			t.Errorf("%v sent the idempotency key %q", tc.args, r.Idempotency)
		}
	}
	for _, args := range [][]string{
		{"show", "all"}, {"account", "enable", "now"}, {"personal", "disable", "me"},
	} {
		if _, err := c.run("", append([]string{"preview"}, args...)...); err == nil {
			t.Errorf("preview %v with an argument ran", args)
		}
	}

	// A server that refuses every setting, as it refuses a member who is no
	// account admin and an API key that is not personal.
	r := chi.NewRouter()
	r.Post(sdk.ExchangePath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jwt":"`+jwt+`","validUntil":4102444800}`)
	})
	r.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusForbidden, "forbidden")
	})
	refusing := httptest.NewServer(r)
	t.Cleanup(refusing.Close)
	for _, args := range [][]string{{"account", "enable"}, {"personal", "disable"}, {"show"}} {
		out, err := c.run("", append([]string{"--endpoint", refusing.URL, "preview"}, args...)...)
		if !errors.As(err, &sdk.Forbidden{}) || !strings.Contains(out, `"status": 403`) {
			t.Errorf("preview %v refused = %q, %v", args, out, err)
		}
	}
}

// gated is a CLI whose tree holds canary, which the feature preview
// "canary-preview" gates, and lab with lab run, which the group's
// annotation of "lab-preview" gates. ran counts the gated commands that
// ran.
func gated(t *testing.T) (*cli, *server, *int) {
	t.Helper()
	c, s := operator(t)
	ran := new(int)
	count := func(*cobra.Command, []string) error {
		*ran++
		return nil
	}
	c.root = func() *cobra.Command {
		root := Root()
		lab := &cobra.Command{
			Use:         "lab",
			Short:       "A group in preview",
			Annotations: map[string]string{previewAnnotation: "lab-preview"},
		}
		lab.AddCommand(&cobra.Command{Use: "run", Short: "A command of the group", RunE: count})
		root.AddCommand(&cobra.Command{
			Use:         "canary",
			Short:       "A command in preview",
			Annotations: map[string]string{previewAnnotation: "canary-preview"},
			Args:        cobra.NoArgs,
			RunE:        count,
		}, lab)
		return root
	}
	return c, s, ran
}

// previews is featurePreview's answer with canary-preview and lab-preview
// active as given.
func previews(canary, lab bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return `{"features":[{"key":"canary-preview","name":"Canary","active":` + b(canary) +
		`},{"key":"lab-preview","name":"Lab","active":` + b(lab) + `}],` +
		`"account":{"enabled":true,"canManage":false},"visible":true}`
}

// listed is whether help lists each command name.
func listed(t *testing.T, c *cli, args []string, names ...string) []bool {
	t.Helper()
	out := c.must("", args...)
	got := make([]bool, len(names))
	for i, n := range names {
		got[i] = strings.Contains(out, "\n  "+n+" ")
	}
	return got
}

func TestUnitPreviewGate(t *testing.T) {
	c, s, ran := gated(t)

	s.setPreviews(previews(false, false), 0)
	for _, help := range [][]string{{"--help"}, {"help"}} {
		if got := listed(t, c, help, "canary", "lab", "preview"); got[0] || got[1] || !got[2] {
			t.Errorf("%v with both previews inactive lists canary, lab, preview = %v", help, got)
		}
	}
	for _, args := range [][]string{{"canary"}, {"lab", "run"}} {
		if _, err := c.run("", args...); !errors.Is(err, ErrPreviewInactive) ||
			!strings.Contains(err.Error(), "preview account enable") {
			t.Errorf("%v with its preview inactive = %v", args, err)
		}
	}
	if *ran != 0 {
		t.Errorf("%d gated commands ran while inactive", *ran)
	}

	s.setPreviews(previews(true, false), 0)
	if got := listed(t, c, []string{"--help"}, "canary", "lab"); !got[0] || got[1] {
		t.Errorf("help with canary-preview alone active lists canary, lab = %v", got)
	}
	c.must("", "canary")
	if _, err := c.run("", "lab", "run"); !errors.Is(err, ErrPreviewInactive) {
		t.Errorf("lab run with lab-preview inactive = %v", err)
	}
	s.setPreviews(previews(true, true), 0)
	if got := listed(t, c, []string{"help"}, "canary", "lab"); !got[0] || !got[1] {
		t.Errorf("help with both previews active lists canary, lab = %v", got)
	}
	c.must("", "lab", "run")
	if *ran != 2 {
		t.Errorf("%d gated commands ran once active, want 2", *ran)
	}

	// A preview the read does not list is not active.
	s.setPreviews(noPreviews, 0)
	if got := listed(t, c, []string{"--help"}, "canary"); got[0] {
		t.Error("help lists canary while the read lists no preview")
	}
	if _, err := c.run("", "canary"); !errors.Is(err, ErrPreviewInactive) {
		t.Errorf("canary while the read lists no preview = %v", err)
	}

	// A command no preview gates runs without reading the previews, and
	// help reads them only for a tree that holds a gated command.
	before := s.count()
	c.must("", "version")
	plain := c.root
	c.root = Root
	c.must("", "--help")
	c.root = plain
	if n := s.count() - before; n != 0 {
		t.Errorf("version and help with no gated command sent %d requests", n)
	}
}

func TestUnitPreviewGateFails(t *testing.T) {
	c, s, ran := gated(t)

	// A refused read hides the gated commands, prints its problem for one
	// that runs and returns the SDK's error.
	s.setPreviews("", http.StatusForbidden)
	if got := listed(t, c, []string{"--help"}, "canary", "lab"); got[0] || got[1] {
		t.Errorf("help with the read refused lists canary, lab = %v", got)
	}
	out, err := c.run("", "canary")
	if !errors.As(err, &sdk.Forbidden{}) || !strings.Contains(out, `"status": 403`) {
		t.Errorf("canary with the read refused = %q, %v", out, err)
	}
	root := c.root()
	root.SetOut(failing{})
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--config", c.config, "canary"})
	if err := root.ExecuteContext(t.Context()); err == nil || errors.As(err, &sdk.Forbidden{}) {
		t.Errorf("canary printing the refusal to a writer that fails = %v", err)
	}

	s.setPreviews("not json", 0)
	if _, err := c.run("", "canary"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("canary with a read that is not JSON = %v", err)
	}

	// With no endpoint, help hides the gated commands and running one
	// fails before any call.
	c.must("", "profile", "delete", "ops")
	if got := listed(t, c, []string{"--help"}, "canary", "lab"); got[0] || got[1] {
		t.Errorf("help with no endpoint lists canary, lab = %v", got)
	}
	if _, err := c.run("", "canary"); !errors.Is(err, ErrNoEndpoint) {
		t.Errorf("canary with no endpoint = %v", err)
	}
	if *ran != 0 {
		t.Errorf("%d gated commands ran", *ran)
	}
}
