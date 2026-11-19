package bitbucketcloud_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketcloud"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Atlassian's Pull Request webhook entity documents a twelve-digit immutable
// source commit, independently of the full object identity used by VCS core.
// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#Pull-request
func TestUnitCloudDocumentedAbbreviatedRequestHead(t *testing.T) {
	for _, kind := range []string{"pullrequest:created", "pullrequest:updated", "pullrequest:approved", "pullrequest:comment_created"} {
		t.Run(kind, func(t *testing.T) {
			body := cloudBody(t, kind, func(d map[string]any) {
				d["pullrequest"].(map[string]any)["source"].(map[string]any)["commit"].(map[string]any)["hash"] = "d3022fc0ca3d"
			})
			p := bitbucketcloud.New(bitbucketcloud.Options{})
			events, err := p.Parse(cloudHeaders(kind), body)
			if err != nil || len(events) != 1 {
				t.Fatal("documented native request commit was rejected before preparation", err)
			}
			if events[0].Head != "d3022fc0ca3d" {
				t.Fatal("native immutable prefix was replaced before preparation")
			}
			if kind != "pullrequest:comment_created" {
				source := events[0].PullRequest.HeadRepository
				if source == nil || source.ID != "{00000000-0000-4000-8000-000000000009}" || source.Namespace != "contributor" || source.Name != "infra" {
					t.Fatal("source identity was lost or substituted with destination")
				}
			}
		})
	}
}

func TestUnitCloudRequestHeadUsesNativeSourceUUID(t *testing.T) {
	l := newLab(t)
	l.fake.Grant("contributor", "different-fork", email, bitbucketcloudfake.Read)
	head := l.fake.Repo("contributor", "different-fork").Commit("feature", "fork commit", map[string]string{"main.tf": "fork"})
	source, err := l.bb.ResolveRepositoryRef(t.Context(), port.Repository{Namespace: "contributor", Name: "different-fork", Integration: &l.in}, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if source.Repository.ID == "" || source.Repository.Namespace != "contributor" || source.Repository.Name != "different-fork" {
		t.Fatal("native source fixture missing")
	}
	// The stable UUID, rather than a stale or destination slug, owns resolution.
	source.Repository.Name = "renamed-since-delivery"
	for _, spelling := range []string{source.Repository.ID, strings.ToUpper(strings.Trim(source.Repository.ID, "{}"))} {
		repo := source.Repository
		repo.ID = spelling
		before := l.fake.Requests()
		got, err := l.bb.ResolveRequestHead(t.Context(), l.in, repo, head[:12])
		if err != nil || got != head || l.fake.Requests() != before+1 {
			t.Fatal("immutable source resolution used another repository or extra native calls", err)
		}
	}
	foreign := integrationOf("foreign@example.com", l.fake.AddToken("foreign@example.com"))
	if head, err := l.bb.ResolveRequestHead(t.Context(), foreign, source.Repository, head[:12]); !errors.Is(err, port.ErrNotFound) || head != "" {
		t.Fatal("foreign token reached a private source repository")
	}
}

func TestUnitCloudRequestHeadCommitWireAndFailures(t *testing.T) {
	const prefix = "d3022fc0ca3d"
	for _, name := range []string{"immutable commit", "matching uppercase", "matching sha256", "nonmatching commit", "partial commit", "zero commit", "ambiguous", "not found", "unavailable", "permission", "rate limited"} {
		t.Run(name, func(t *testing.T) {
			calls, branchCalls := 0, 0
			r := chi.NewRouter()
			full := prefix + strings.Repeat("a", 28)
			r.Get("/2.0/repositories/{workspace}/{repo}/refs/branches/{name}", func(w http.ResponseWriter, _ *http.Request) {
				branchCalls++
				_ = json.NewEncoder(w).Encode(map[string]any{"target": map[string]string{"hash": strings.Repeat("b", 40)}})
			})
			r.Get("/2.0/repositories/{workspace}/{repo}/commit/{prefix}", func(w http.ResponseWriter, req *http.Request) {
				calls++
				user, token, ok := req.BasicAuth()
				if !ok || user != email || token != "private-test-token" || chi.URLParam(req, "workspace") != "contributor" ||
					chi.URLParam(req, "repo") != "{00000000-0000-4000-8000-000000000009}" ||
					chi.URLParam(req, "prefix") != prefix {
					t.Error("head lookup changed identity, immutable ref or authentication")
				}
				status, hash := 200, full
				switch name {
				case "matching uppercase":
					hash = strings.ToUpper(full)
				case "matching sha256":
					hash = prefix + strings.Repeat("a", 52)
				case "nonmatching commit":
					hash = strings.Repeat("b", 40)
				case "partial commit":
					hash = prefix
				case "zero commit":
					hash = strings.Repeat("0", 40)
				case "ambiguous":
					status = 400
				case "not found":
					status = 404
				case "unavailable":
					status = 502
				case "permission":
					status = 403
				case "rate limited":
					status = 429
					w.Header().Set("Retry-After", "10")
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"hash": hash, "error": map[string]string{"message": "native refusal"}})
			})
			srv := httptest.NewServer(r)
			defer srv.Close()
			p := bitbucketcloud.New(bitbucketcloud.Options{APIHost: srv.URL + "/2.0"})
			repo := vcsapi.Repository{ID: "{00000000-0000-4000-8000-000000000009}", Namespace: "contributor", Name: "different-fork"}
			got, err := p.ResolveRequestHead(t.Context(), integrationOf(email, "private-test-token"), repo, prefix)
			var want error
			switch name {
			case "nonmatching commit", "partial commit", "zero commit", "unavailable":
				want = kernel.ErrUnavailable
			case "ambiguous":
				want = bitbucketcloud.ErrStatus
			case "not found":
				want = port.ErrNotFound
			case "permission":
				want = port.ErrCredential
			}
			if name == "rate limited" {
				if _, ok := errors.AsType[*port.RateLimited](err); !ok || got != "" {
					t.Fatal("rate limit lost scheduling type")
				}
			} else if !errors.Is(err, want) {
				t.Fatal("lost native failure category", err)
			}
			if calls != 1 || branchCalls != 0 {
				t.Fatal("immutable request followed a hexadecimal branch or repeated native lookup")
			}
			if err == nil {
				if !port.ValidCommit(got) || !strings.HasPrefix(strings.ToLower(got), prefix) {
					t.Fatal("invalid resolved head accepted")
				}
			} else if got != "" {
				t.Fatal("failed resolution returned a usable head")
			}
		})
	}
}

type requestHeadTransport func(*http.Request) (*http.Response, error)

func (f requestHeadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnitCloudRequestHeadRejectsMalformedIdentityBeforeTransport(t *testing.T) {
	calls := 0
	cause := errors.New("native transport unavailable")
	p := bitbucketcloud.NewOver(requestHeadTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, cause }), bitbucketcloud.DefaultAPIHost)
	valid := vcsapi.Repository{ID: "{00000000-0000-4000-8000-000000000009}", Namespace: "contributor", Name: "different-fork"}
	for _, name := range []string{"missing id", "invalid id", "zero id", "missing namespace", "invalid namespace", "missing name", "invalid name", "short ref", "mutable ref", "nonhex ref", "zero ref"} {
		t.Run(name, func(t *testing.T) {
			repo, prefix := valid, "d3022fc0ca3d"
			switch name {
			case "missing id":
				repo.ID = ""
			case "invalid id":
				repo.ID = "another-repository"
			case "zero id":
				repo.ID = "{00000000-0000-0000-0000-000000000000}"
			case "missing namespace":
				repo.Namespace = ""
			case "invalid namespace":
				repo.Namespace = "../other"
			case "missing name":
				repo.Name = ""
			case "invalid name":
				repo.Name = "other/name"
			case "short ref":
				prefix = "d3022f"
			case "mutable ref":
				prefix = "refs/heads/main"
			case "nonhex ref":
				prefix = "zzzzzzzzzzzz"
			case "zero ref":
				prefix = strings.Repeat("0", 12)
			}
			if head, err := p.ResolveRequestHead(
				t.Context(),
				integrationOf(email, "private-test-token"),
				repo,
				prefix,
			); !errors.Is(err, bitbucketcloud.ErrWebhookPayload) || head != "" ||
				calls != 0 {
				t.Fatal("malformed identity/ref reached transport")
			}
		})
	}
	for _, full := range []string{strings.Repeat("a", 40), strings.Repeat("b", 64)} {
		if head, err := p.ResolveRequestHead(t.Context(), port.Integration{}, vcsapi.Repository{}, full); err != nil || head != full || calls != 0 {
			t.Fatal("complete native head acquired a new dependency")
		}
	}
	if head, err := p.ResolveRequestHead(
		context.Background(),
		integrationOf(email, "private-test-token"),
		valid,
		"d3022fc0ca3d",
	); !errors.Is(err, cause) || !errors.Is(err, kernel.ErrUnavailable) || head != "" ||
		calls != 1 {
		t.Fatal("genuine transport failure lost retryable cause")
	}
}
