package index_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index/indextest"
)

// serve serves h for the test's length.
func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestUnitFixtures holds each reader to the contract over its recorded
// index, served as the vendor serves it.
func TestUnitFixtures(t *testing.T) {
	tf, err := indextest.Load("testdata/terraform.json")
	if err != nil {
		t.Fatal(err)
	}
	tofu, err := indextest.Load("testdata/opentofu.json")
	if err != nil {
		t.Fatal(err)
	}
	hc := index.HashiCorp{URL: serve(t, indextest.HashiCorp(tf.Body)) + "/v1/releases/terraform"}
	indextest.Run(t, hc, indextest.TerraformFacts)
	indextest.Run(
		t,
		index.OpenTofu{URL: serve(t, indextest.OpenTofu(tofu.Body))},
		indextest.OpenTofuFacts,
	)

	// The fixture's every stable release is listed, across both of its
	// pages, each with the time it was created.
	var recorded []struct {
		Version    string `json:"version"`
		Prerelease bool   `json:"is_prerelease"` //nolint:tagliatelle // HashiCorp's spelling
	}
	if err := json.Unmarshal(tf.Body, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) <= index.HashiCorpPage {
		t.Fatalf("the fixture holds %d releases, one page", len(recorded))
	}
	stable := 0
	for _, r := range recorded {
		if !r.Prerelease {
			stable++
		}
	}
	rs, err := hc.Releases(t.Context())
	if err != nil || len(rs) != stable {
		t.Fatalf("%d releases, %v; want %d", len(rs), err, stable)
	}
	for _, r := range rs {
		if r.Published.IsZero() {
			t.Errorf("%s has no time", r.Version)
		}
	}
}

// TestUnitHashiCorpSkips lists only stable releases: none HashiCorp marks
// a prerelease or withdrew, and none whose version is a prerelease or does
// not parse.
func TestUnitHashiCorpSkips(t *testing.T) {
	body := `[
		{"version":"1.2.0","is_prerelease":false,"timestamp_created":"2024-01-02T03:04:05.678Z","status":{"state":"supported"}},
		{"version":"1.3.0-rc1","is_prerelease":true,"timestamp_created":"2024-01-01T00:00:00Z","status":{"state":"supported"}},
		{"version":"1.1.0","is_prerelease":false,"timestamp_created":"2023-12-01T00:00:00Z","status":{"state":"withdrawn"}},
		{"version":"1.0.0-beta1","is_prerelease":false,"timestamp_created":"2023-11-01T00:00:00Z","status":{"state":"supported"}},
		{"version":"v-one","is_prerelease":false,"timestamp_created":"2023-10-01T00:00:00Z","status":{"state":"supported"}},
		{"version":"1.0.0","is_prerelease":false,"timestamp_created":"yesterday","status":{"state":"supported"}}
	]`
	rs, err := index.HashiCorp{URL: serve(t, indextest.HashiCorp(json.RawMessage(body)))}.
		Releases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(rs))
	for i, r := range rs {
		got[i] = r.Version.String()
	}
	if !slices.Equal(got, []string{"1.2.0", "1.0.0"}) {
		t.Errorf("releases %v", got)
	}
	want := time.Date(2024, 1, 2, 3, 4, 5, 678e6, time.UTC)
	if !rs[0].Published.Equal(want) || !rs[1].Published.IsZero() {
		t.Errorf("times %v, %v", rs[0].Published, rs[1].Published)
	}
}

// TestUnitOpenTofuDuplicates lists a version the index names twice once.
func TestUnitOpenTofuDuplicates(t *testing.T) {
	body := `{"versions":[{"id":"1.6.0"},{"id":"1.7.0"},{"id":"1.6.0"},{"id":"1.8.0-beta1"}]}`
	rs, err := index.OpenTofu{URL: serve(t, indextest.OpenTofu(json.RawMessage(body)))}.
		Releases(t.Context())
	if err != nil || len(rs) != 2 || rs[0].Version.String() != "1.7.0" {
		t.Errorf("releases %+v, %v", rs, err)
	}
}

// TestUnitErrors names the index a read fails on, and why, as ErrIndex.
func TestUnitErrors(t *testing.T) {
	answer := func(status int, body string) string {
		return serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	full := make([]string, index.HashiCorpPage)
	for i := range full {
		full[i] = fmt.Sprintf(`{"version":"1.0.%d","timestamp_created":"2024-01-01T00:00:00Z"}`, i)
	}
	endless := answer(http.StatusOK, "["+strings.Join(full, ",")+"]")
	for name, c := range map[string]struct {
		r    index.Reader
		want string
	}{
		"a failing server":   {index.OpenTofu{URL: answer(http.StatusBadGateway, "")}, "502"},
		"a body not JSON":    {index.HashiCorp{URL: answer(http.StatusOK, "<html>")}, "invalid character"},
		"no server":          {index.OpenTofu{URL: gone.URL}, "connection refused"},
		"no stable release":  {index.OpenTofu{URL: answer(http.StatusOK, `{"versions":[]}`)}, "no stable release"},
		"a URL that is none": {index.HashiCorp{URL: "http://[::1"}, "missing ']'"},
		"a bad request URL":  {index.OpenTofu{URL: "http://a b"}, "invalid character"},
		"endless pages":      {index.HashiCorp{URL: endless}, "more than 500 pages"},
	} {
		_, err := c.r.Releases(t.Context())
		if !errors.Is(err, index.ErrIndex) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrIndex naming %q", name, err, c.want)
		}
	}
}

// TestAccVendorsUpstream holds each reader to the contract over the
// vendor's real index. It reaches the internet, so it runs only where
// INFRAWEAVE_TEST_VENDORS_UPSTREAM is set: the nightly vendors-upstream
// job.
func TestAccVendorsUpstream(t *testing.T) {
	if os.Getenv("INFRAWEAVE_TEST_VENDORS_UPSTREAM") == "" {
		t.Skip("INFRAWEAVE_TEST_VENDORS_UPSTREAM is not set")
	}
	client := &http.Client{Timeout: time.Minute}
	t.Run("terraform", func(t *testing.T) {
		indextest.Run(t, index.HashiCorp{URL: index.DefaultTerraformURL, Client: client},
			indextest.TerraformFacts)
	})
	t.Run("opentofu", func(t *testing.T) {
		indextest.Run(t, index.OpenTofu{URL: index.DefaultOpenTofuURL, Client: client},
			indextest.OpenTofuFacts)
	})
}
