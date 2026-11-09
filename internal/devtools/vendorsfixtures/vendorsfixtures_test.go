package vendorsfixtures_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/devtools/vendorsfixtures"
)

// releases are 25 releases newest first, as HashiCorp's API lists them:
// 1.0.0 to 1.0.19 and the prereleases 2.0.0-rc1, 2.0.0-beta1, 1.1.0-rc1,
// 0.9.0 and one that does not parse.
func releases() []map[string]any {
	var out []map[string]any
	add := func(v string, i int) {
		out = append(out, map[string]any{
			"version": v, "builds": []string{"linux"},
			"timestamp_created": fmt.Sprintf("2024-01-01T00:00:%02dZ", 59-i),
		})
	}
	for i, v := range []string{"2.0.0-rc1", "2.0.0-beta1", "1.1.0-rc1", "?"} {
		add(v, i)
	}
	for n := 19; n >= 0; n-- {
		add("1.0."+strconv.Itoa(n), len(out))
	}
	add("0.9.0", len(out))
	return out
}

// hashicorp pages all as HashiCorp's API does.
func hashicorp(all []map[string]any) http.Handler {
	r := chi.NewRouter()
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		after := req.URL.Query().Get("after")
		page := []map[string]any{}
		for _, rel := range all {
			if after == "" || rel["timestamp_created"].(string) < after {
				page = append(page, rel)
			}
		}
		_ = json.NewEncoder(w).Encode(page[:min(limit, len(page))])
	})
	return r
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestUnitRun keeps each index's fixed set and its newest stable release
// and prerelease, in the index's order, without the downloads it lists.
func TestUnitRun(t *testing.T) {
	tofu := `{"versions":[{"id":"1.7.0","files":["a","b","c"]},{"id":"1.6.1","files":["a"]},` +
		`{"id":"1.8.0-beta1","files":[]},{"id":"1.5.0","files":[]}],"extra":true}`
	sources := []vendorsfixtures.Source{
		{
			Name: "terraform", Format: vendorsfixtures.HashiCorp,
			URL:  serve(t, hashicorp(releases())) + "/v1/releases/terraform",
			Keep: regexp.MustCompile(`^1\.0\.1[0-2]$`),
		},
		{
			Name: "opentofu", Format: vendorsfixtures.OpenTofu,
			URL: serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tofu))
			})),
			Keep: regexp.MustCompile(`^1\.6\.`),
		},
	}
	dir := t.TempDir()
	if err := vendorsfixtures.Run(
		t.Context(),
		http.DefaultClient,
		sources,
		dir,
		"2026-11-09",
	); err != nil {
		t.Fatal(err)
	}
	read := func(name string, body any) vendorsfixtures.Fixture {
		raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var f vendorsfixtures.Fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(f.Body, body); err != nil {
			t.Fatal(err)
		}
		return f
	}
	var tf []map[string]any
	f := read("terraform", &tf)
	got := make([]string, 0, len(tf))
	for _, r := range tf {
		got = append(got, r["version"].(string))
		if _, ok := r["builds"]; ok {
			t.Errorf("%s keeps its builds", r["version"])
		}
	}
	if want := []string{
		"2.0.0-rc1",
		"1.0.19",
		"1.0.12",
		"1.0.11",
		"1.0.10",
	}; !slices.Equal(got, want) ||
		f.Fetched != "2026-11-09" ||
		f.Source != sources[0].URL {
		t.Errorf("terraform fixture %v from %s on %s, want %v", got, f.Source, f.Fetched, want)
	}
	var idx struct {
		Versions []struct {
			ID    string   `json:"id"`
			Files []string `json:"files"`
		} `json:"versions"`
		Extra bool `json:"extra"`
	}
	read("opentofu", &idx)
	got = make([]string, 0, len(idx.Versions))
	for _, v := range idx.Versions {
		got = append(got, v.ID)
	}
	if !slices.Equal(got, []string{"1.7.0", "1.6.1", "1.8.0-beta1"}) || !idx.Extra ||
		len(idx.Versions[0].Files) != 2 {
		t.Errorf("opentofu fixture %+v", idx)
	}
}

// TestUnitRunFails names the source it could not record.
func TestUnitRunFails(t *testing.T) {
	answer := func(status int, body string) string {
		return serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	full := make([]string, 20)
	for i := range full {
		full[i] = `{"version":"1.0.0"}`
	}
	ok := answer(http.StatusOK, `{"versions":[]}`)
	for name, c := range map[string]struct {
		s    vendorsfixtures.Source
		dir  string
		want string
	}{
		"a failing index":   {vendorsfixtures.Source{Format: vendorsfixtures.OpenTofu, URL: answer(http.StatusBadGateway, "")}, "", "502"},
		"a body not JSON":   {vendorsfixtures.Source{Format: vendorsfixtures.HashiCorp, URL: answer(http.StatusOK, "<")}, "", "invalid character"},
		"no versions":       {vendorsfixtures.Source{Format: vendorsfixtures.OpenTofu, URL: answer(http.StatusOK, `{}`)}, "", "no versions"},
		"a page untimed":    {vendorsfixtures.Source{Format: vendorsfixtures.HashiCorp, URL: answer(http.StatusOK, "["+strings.Join(full, ",")+"]")}, "", "timestamp_created"},
		"an unknown format": {vendorsfixtures.Source{Name: "x", Format: "svn"}, "", `no format "svn"`},
		"no server":         {vendorsfixtures.Source{Format: vendorsfixtures.OpenTofu, URL: "http://127.0.0.1:1"}, "", "connection refused"},
		"a bad URL":         {vendorsfixtures.Source{Format: vendorsfixtures.HashiCorp, URL: "http://[::1"}, "", "missing ']'"},
		"a bad request":     {vendorsfixtures.Source{Format: vendorsfixtures.OpenTofu, URL: "http://a b"}, "", "invalid character"},
		"no directory":      {vendorsfixtures.Source{Name: "x", Format: vendorsfixtures.OpenTofu, URL: ok}, "/nonexistent/dir", "no such file"},
	} {
		if c.s.Keep == nil {
			c.s.Keep = regexp.MustCompile(`^$`)
		}
		dir := c.dir
		if dir == "" {
			dir = t.TempDir()
		}
		err := vendorsfixtures.Run(
			t.Context(),
			http.DefaultClient,
			[]vendorsfixtures.Source{c.s},
			dir,
			"d",
		)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, c.want)
		}
		if name != "no directory" && !errors.Is(err, vendorsfixtures.ErrFetch) {
			t.Errorf("%s: %v is not ErrFetch", name, err)
		}
	}
}
