package github

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type commentRouteProbe struct{ calls int }

func (p *commentRouteProbe) RoundTrip(*http.Request) (*http.Response, error) {
	p.calls++
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestUnitCommentGraphQLTransportScope(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"https://api.github.example/", "https://ghe.example/api/v3/", "https://ghe.example/forge/api/v3/"} {
		h := host{api: base}
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		native := commentGraphQLPath(parsed.Path)
		origin := parsed.Scheme + "://" + parsed.Host

		for _, tc := range []struct {
			target  string
			purpose RoutePurpose
			ok      bool
		}{
			{origin + native, CommentWrite, true},
			{origin + native, allRoutes, true},
			{origin + native, ReadOnly, false},
			{origin + native, DeploymentWrite, false},
			{origin + native + "#fragment", CommentWrite, false},
			{origin + native + "?query=mutation", CommentWrite, false},
			{strings.Replace(origin, "https://", "https://user:secret@", 1) + native, CommentWrite, false},
			{"https://other.example" + native, CommentWrite, false},
			{origin + strings.TrimSuffix(parsed.Path, "/") + "/api/graphql", CommentWrite, false},
		} {
			probe := new(commentRouteProbe)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := guarded(h, probe, tc.purpose).RoundTrip(request)
			if response != nil {
				_ = response.Body.Close()
			}
			if tc.ok {
				if err != nil || probe.calls != 1 {
					t.Fatal("canonical GraphQL route refused", base, tc.target, err)
				}
			} else if !errors.Is(err, ErrRoute) || probe.calls != 0 {
				t.Fatal("GraphQL scope escaped", base, tc.target, err)
			}
		}
	}
}
