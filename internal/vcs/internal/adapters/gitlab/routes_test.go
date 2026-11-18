package gitlab

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

type routeProbe func(*http.Request) (*http.Response, error)

func (f routeProbe) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnitRoutes(t *testing.T) {
	for _, path := range []string{
		"/user", "/personal_access_tokens/self", "/projects", "/projects/acme%2Fplatform%2Finfra/hooks",
		"/projects/7", "/projects/7/repository/commits/main", "/projects/7/repository/tags/v1",
		"/projects/7/repository/files/modules%2Fa.tf/raw", "/projects/7/repository/compare", "/projects/7/repository/branches",
	} {
		if !Allowed(http.MethodGet, path) {
			t.Errorf("allowed read refused: %s", path)
		}
	}
	for _, tc := range [][2]string{{http.MethodPost, "/projects/7/access_tokens"}, {http.MethodDelete, "/projects/7/access_tokens/1"}} {
		if !Allowed(tc[0], tc[1]) {
			t.Fatal(tc)
		}
	}
	for _, tc := range [][2]string{
		{http.MethodPost, "/projects/7/hooks"},
		{http.MethodDelete, "/projects/7/hooks/1"},
		{http.MethodGet, "/projects/../user"},
		{http.MethodGet, "/user?token=secret"},
		{http.MethodGet, "https://elsewhere.example/user"},
		{http.MethodGet, "%invalid"},
		{http.MethodGet, ""},
	} {
		if Allowed(tc[0], tc[1]) {
			t.Errorf("unexpected route: %v", tc)
		}
	}
	base, _ := url.Parse("https://gitlab.example/api/v4/")
	sent := 0
	tr := routeTransport{base: base, next: routeProbe(func(*http.Request) (*http.Response, error) { sent++; return nil, nil })}
	for _, raw := range []string{"https://elsewhere.example/api/v4/user", "http://gitlab.example/api/v4/user", "https://gitlab.example/outside/user", "https://gitlab.example/api/v4/groups"} {
		u, _ := url.Parse(raw)
		if _, err := tr.RoundTrip(&http.Request{Method: http.MethodGet, URL: u}); !errors.Is(err, ErrRoute) {
			t.Fatal(raw, err)
		}
	}
	u, _ := url.Parse("https://gitlab.example/api/v4/projects/acme%2Finfra/hooks?page=2")
	if _, err := tr.RoundTrip(&http.Request{Method: http.MethodGet, URL: u}); err != nil || sent != 1 {
		t.Fatalf("allowed SDK route: sent=%d %v", sent, err)
	}
}
