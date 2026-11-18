package azuredevops_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitImmutableAzureTagObjects(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	repo := l.fake.Repo("Platform", "infra")
	object, _, _, err := repo.TagOf("v1")
	if err != nil {
		t.Fatal(err)
	}
	// A different currently named tag points to a later commit. Resolution
	// receives only the delivered old object ID, never a mutable ref name.
	repo.Tag("current", l.repo.Head, "later tag")
	for _, tc := range []struct{ name, head, want string }{
		{"annotated", object, l.repo.Tagged}, {"lightweight", l.repo.Head, l.repo.Head},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := l.fake.Requests()
			got, err := l.az.ResolveTag(ctx, l.repo.Repo, tc.head)
			if err != nil || got.Hash != tc.want || got.Message == "" || got.AuthorName == "" || got.Timestamp.IsZero() || !strings.Contains(got.URL, tc.want) ||
				l.fake.Requests()-before != 2 {
				t.Fatal("immutable tag did not resolve complete commit metadata in two bounded calls", got, err)
			}
		})
	}
	if _, err := l.az.ResolveTag(ctx, l.repo.Missing, object); !errors.Is(err, port.ErrNotFound) {
		t.Fatal("unreadable repository was exposed", err)
	}
	for _, fault := range []azuredevopsfake.Fault{azuredevopsfake.ServerError, azuredevopsfake.RateLimited} {
		l.fake.Fail(fault, 0)
		before := l.fake.Requests()
		_, err := l.az.ResolveTag(ctx, l.repo.Repo, object)
		if err == nil || l.fake.Requests()-before != 1 {
			t.Fatal("failed native lookup fell back or retried", err)
		}
	}
	l.fake.Fail(azuredevopsfake.None, 0)
	if _, err := l.az.ResolveTag(ctx, l.repo.Repo, strings.Repeat("f", 40)); !errors.Is(err, port.ErrNotFound) {
		t.Fatal("unknown immutable object accepted", err)
	}
	before := l.fake.Requests()
	if _, err := l.az.ResolveTag(ctx, l.repo.Repo, "v1"); !errors.Is(err, kernel.ErrUnavailable) || l.fake.Requests() != before {
		t.Fatal("mutable tag name was resolved", err)
	}
}

func TestUnitAzureMalformedTagChainsFailClosed(t *testing.T) {
	for _, fault := range []string{"cycle", "wrong object", "tree target", "deep chain"} {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			router := chi.NewRouter()
			router.Get("/acme/Platform/_apis/git/repositories/infra/annotatedtags/{object}", func(w http.ResponseWriter, r *http.Request) {
				calls++
				head := chi.URLParam(r, "object")
				id, target, kind := head, head, "tag"
				switch fault {
				case "wrong object":
					id = strings.Repeat("f", 40)
				case "tree target":
					kind = "tree"
				case "deep chain":
					target = fmt.Sprintf("%040x", calls+10)
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"objectId": id, "taggedObject": map[string]string{"objectId": target, "objectType": kind}}); err != nil {
					t.Error(err)
				}
			})
			srv := httptest.NewTLSServer(router)
			defer srv.Close()
			provider := azuredevops.NewOver(srv.Client().Transport)
			repo := port.Repository{
				Namespace:   "Platform",
				Name:        "infra",
				Integration: &port.Integration{Settings: port.Settings{OrganizationURL: srv.URL + "/acme"}, Credential: []byte("native-tag-token")},
			}
			_, err := provider.ResolveTag(context.Background(), repo, strings.Repeat("a", 40))
			if !errors.Is(err, kernel.ErrUnavailable) || calls == 0 || calls > 16 {
				t.Fatal("untrusted tag chain escaped bound/refusal", calls, err)
			}
			if fault == "deep chain" && calls != 16 {
				t.Fatal("deep chain was silently truncated rather than refused", calls)
			}
		})
	}
}
