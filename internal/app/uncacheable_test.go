package app

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/ratelimit"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// blobKeys derives one fixed objectstore-url key, ID 1.
type blobKeys struct{}

func (blobKeys) DeriveKey(string) ([]byte, uint32, error) {
	return bytes.Repeat([]byte{1}, 32), 1, nil
}

func (blobKeys) DerivedKey(string, uint32) ([]byte, error) {
	return bytes.Repeat([]byte{1}, 32), nil
}

// wantUncacheable checks that rec carries both marks of an API answer.
func wantUncacheable(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cc, sniff := rec.Header().Get("Cache-Control"),
		rec.Header().Get("X-Content-Type-Options"); cc != "no-store" || sniff != "nosniff" {
		t.Errorf("%s (%d): Cache-Control %q, X-Content-Type-Options %q", what, rec.Code, cc, sniff)
	}
}

// TestUnitUncacheableAnswers checks that every answer under each surface's
// prefix carries Cache-Control: no-store and X-Content-Type-Options:
// nosniff, whichever control or handler gives it: a 200, a 401, a 404, a
// 429 and a panic's 500; and that the served API's answers and a signed
// blob GET of the fs driver carry them too.
func TestUnitUncacheableAnswers(t *testing.T) {
	c := config.Defaults().Server
	c.RateLimit.Standard = ratelimit.Limit{Rate: 1e-6, Burst: 1}
	ops := Operations{}
	for _, base := range []string{BaseURL, OperatorBaseURL, WorkerBaseURL, stateapi.BaseURL} {
		ops["GET "+base+"/ok"] = Operation{Cost: ratelimit.Standard}
		ops["GET "+base+"/boom"] = Operation{Cost: ratelimit.Standard}
	}
	route := func(r interface {
		Get(string, http.HandlerFunc)
	},
	) {
		for _, base := range []string{BaseURL, OperatorBaseURL, WorkerBaseURL, stateapi.BaseURL} {
			r.Get(base+"/ok", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			r.Get(base+"/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
		}
	}
	admitted := Guarded(discard(), c, ops, memory(c), byAddress{}, nil, nil)
	route(admitted)
	refused := Guarded(discard(), c, ops, memory(c), nil, nil, nil)
	route(refused)
	sent := 0
	for _, base := range []string{BaseURL, OperatorBaseURL, WorkerBaseURL, stateapi.BaseURL} {
		// Each request is its own caller but the 429, which repeats the
		// 200's to drain its bucket of one.
		for _, tc := range []struct {
			h      http.Handler
			path   string
			status int
		}{
			{admitted, "/ok", http.StatusOK},
			{admitted, "/ok", http.StatusTooManyRequests},
			{refused, "/ok", http.StatusUnauthorized},
			{admitted, "/nope", http.StatusNotFound},
			{admitted, "/boom", http.StatusInternalServerError},
		} {
			if tc.status != http.StatusTooManyRequests {
				sent++
			}
			rec := send(
				tc.h,
				http.MethodGet,
				base+tc.path,
				fmt.Sprintf("198.51.100.%d:1", sent),
				"",
			)
			if rec.Code != tc.status {
				t.Errorf("GET %s%s: %d, want %d", base, tc.path, rec.Code, tc.status)
			}
			wantUncacheable(t, "GET "+base+tc.path, rec)
		}
	}

	b, err := fs.New(objectstore.FSConfig{Root: filepath.Join(t.TempDir(), "objects")}, blobKeys{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if _, err := b.Put(t.Context(), "s/a", strings.NewReader("abc"), 3); err != nil {
		t.Fatal(err)
	}
	signed, err := b.SignURL(t.Context(), http.MethodGet, "s/a", time.Minute,
		objectstore.Constraints{})
	if err != nil {
		t.Fatal(err)
	}
	c = config.Defaults().Server
	h := API(discard(), c, memory(c), Domains{Auth: byAddress{}}, Platform{},
		Blobs{Routes: b.Routes})
	rec := request(t, h, http.MethodGet, signed.URL)
	if rec.Code != http.StatusOK || rec.Body.String() != "abc" {
		t.Errorf("a signed blob GET = %d %q", rec.Code, rec.Body)
	}
	wantUncacheable(t, "a signed blob GET", rec)
	for _, target := range []string{
		BaseURL + "/integrations-count", OperatorBaseURL + "/organizations",
		WorkerBaseURL + "/runs/" + "01KACME000000000000000000A" + "/spec",
		stateapi.BaseURL + "/stacks/one",
	} {
		wantUncacheable(t, "GET "+target, request(t, h, http.MethodGet, target))
	}
}
