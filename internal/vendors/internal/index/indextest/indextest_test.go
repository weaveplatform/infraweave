package indextest_test

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

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index/indextest"
)

// listed is a reader that lists versions in its order, or fails with err.
type listed struct {
	versions []string
	err      error
}

func (l listed) Releases(context.Context) ([]index.Release, error) {
	out := make([]index.Release, len(l.versions))
	for i, s := range l.versions {
		v, err := version.Parse(s)
		if err != nil {
			return nil, err
		}
		out[i] = index.Release{Version: v}
	}
	return out, l.err
}

// TestUnitCheck passes a reader that keeps the contract and names each
// way a reader can break it.
func TestUnitCheck(t *testing.T) {
	good := listed{versions: []string{"1.6.0", "1.5.7", "1.5.0"}}
	indextest.Run(t, good, indextest.TerraformFacts)
	for name, c := range map[string]struct {
		r    index.Reader
		f    indextest.Facts
		want string
	}{
		"a failed read":      {listed{err: errors.New("down")}, indextest.Facts{}, "down"},
		"nothing listed":     {listed{}, indextest.Facts{}, "no release listed"},
		"a prerelease":       {listed{versions: []string{"1.6.0-rc1"}}, indextest.Facts{}, "prerelease 1.6.0-rc1"},
		"oldest first":       {listed{versions: []string{"1.5.0", "1.6.0"}}, indextest.Facts{}, "not newest first"},
		"one listed twice":   {listed{versions: []string{"1.5.0", "1.5.0"}}, indextest.Facts{}, "not newest first"},
		"a missing release":  {good, indextest.Facts{Listed: []string{"1.4.0"}}, "1.4.0 not listed"},
		"a wrong resolution": {good, indextest.Facts{Resolves: []indextest.Resolution{{"< 1.6.0", "1.5.0"}}}, `want "1.5.0"`},
		"a bad constraint":   {good, indextest.Facts{Resolves: []indextest.Resolution{{"one", "1.5.0"}}}, "not a version"},
	} {
		if err := indextest.Check(t.Context(), c.r, c.f); !errors.Is(err, indextest.ErrContract) ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrContract naming %q", name, err, c.want)
		}
	}
}

// TestUnitLoad reads a fixture and names the one it cannot.
func TestUnitLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"source":"s","fetched":"d","body":[1]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if f, err := indextest.Load(path); err != nil || f.Source != "s" || string(f.Body) != "[1]" {
		t.Errorf("Load = %+v, %v", f, err)
	}
	if err := os.WriteFile(path, []byte(`[`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, filepath.Join(dir, "none.json")} {
		if _, err := indextest.Load(p); err == nil {
			t.Errorf("Load(%s) read a fixture", p)
		}
	}
}

// TestUnitHashiCorp pages as HashiCorp's API does, and refuses the
// requests it would.
func TestUnitHashiCorp(t *testing.T) {
	body := json.RawMessage(`[
		{"version":"3","timestamp_created":"2024-03-01T00:00:00Z"},
		{"version":"2","timestamp_created":"2024-02-01T00:00:00Z"},
		{"version":"1","timestamp_created":"2024-01-01T00:00:00Z"}]`)
	get := func(h http.Handler, query string) (int, string) {
		w := httptest.NewRecorder()
		h.ServeHTTP(
			w,
			httptest.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"/v1/releases/terraform?"+query,
				nil,
			),
		)
		return w.Code, w.Body.String()
	}
	h := indextest.HashiCorp(body)
	for query, want := range map[string]string{
		"limit=2":                             `["3","2"]`,
		"limit=20&after=2024-02-01T00:00:00Z": `["1"]`,
		"limit=20&after=2024-01-01T00:00:00Z": `[]`,
	} {
		code, out := get(h, query)
		var page []struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal([]byte(out), &page)
		got, _ := json.Marshal(func() []string {
			vs := []string{}
			for _, p := range page {
				vs = append(vs, p.Version)
			}
			return vs
		}())
		if code != http.StatusOK || string(got) != want {
			t.Errorf("%s: %d %s, want %s", query, code, got, want)
		}
	}
	for _, query := range []string{"", "limit=21", "limit=1&after=soon"} {
		if code, _ := get(h, query); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", query, code)
		}
	}
	if code, _ := get(
		indextest.HashiCorp(json.RawMessage(`{}`)),
		"limit=1",
	); code != http.StatusInternalServerError {
		t.Errorf("a body not a list: %d", code)
	}
	w := httptest.NewRecorder()
	indextest.OpenTofu(json.RawMessage(`{"versions":[]}`)).
		ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/tofu/api.json", nil))
	if w.Body.String() != `{"versions":[]}` {
		t.Errorf("OpenTofu served %s", w.Body)
	}
}
