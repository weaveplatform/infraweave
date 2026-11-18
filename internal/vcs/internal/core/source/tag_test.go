package source

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type tagSource struct {
	port.Source
	ref    string
	repo   port.Repository
	commit vcsapi.Commit
	err    error
	calls  int
}

func (s *tagSource) ResolveRef(ctx context.Context, repo port.Repository, ref string) (vcsapi.Commit, error) {
	if tx.In(ctx) {
		return vcsapi.Commit{}, errors.New("remote tag resolution held a transaction")
	}
	s.calls++
	s.ref, s.repo = ref, repo
	return s.commit, s.err
}

type peelingTagSource struct {
	*tagSource
	peeled int
}

func (s *peelingTagSource) ResolveTag(ctx context.Context, repo port.Repository, ref string) (vcsapi.Commit, error) {
	s.peeled++
	return s.ResolveRef(ctx, repo, ref)
}

func TestUnitPrepareTagImmutableMetadata(t *testing.T) {
	for _, peeling := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "annotated object"}[peeling], func(t *testing.T) {
			t.Parallel()
			commit := vcsapi.Commit{
				Hash:        strings.Repeat("b", 40),
				Tag:         "mutable-name-must-not-win",
				URL:         "https://example.com/commit",
				Message:     "summary",
				AuthorName:  "Creator",
				AuthorLogin: new("creator"),
				Timestamp:   time.Now().UTC(),
			}
			provider := &tagSource{commit: commit}
			var src port.Source = provider
			peeled := &peelingTagSource{tagSource: provider}
			if peeling {
				src = peeled
			}
			id := kernel.NewULID()
			var asked *kernel.ULID
			s := New(Deps{Providers: map[string]port.Source{"TEST": src}, Integrations: integrations{in: port.Integration{ID: id}, found: true, asked: &asked}})
			event := vcsapi.Event{Type: "tag", Tag: "delivered-v1", Head: strings.Repeat("a", 40), Repository: vcsapi.Repository{Namespace: "acme", Name: "infra"}}
			got, err := s.PrepareTag(t.Context(), kernel.NewAccountID(), id, "TEST", event)
			want := stackCommit(commit)
			want.Tag = "delivered-v1"
			if err != nil || !reflect.DeepEqual(got, want) || provider.ref != event.Head || provider.calls != 1 || provider.repo.Namespace != "acme" ||
				provider.repo.Name != "infra" ||
				provider.repo.Integration.ID != id ||
				asked == nil ||
				*asked != id {
				t.Fatal("tag lost immutable identity/full metadata or reread mutable name", got, provider, err)
			}
			if peeling && peeled.peeled != 1 {
				t.Fatal("provider object peeling seam was bypassed")
			}
		})
	}
}

func TestUnitPrepareTagRefusals(t *testing.T) {
	for _, fault := range []string{"provider", "integration", "missing integration", "source", "empty head", "zero head", "nonhex head"} {
		t.Run(fault, func(t *testing.T) {
			cause := errors.New("source down")
			provider := &tagSource{}
			binding := integrations{found: true}
			providers := map[string]port.Source{"TEST": provider}
			head := strings.Repeat("a", 40)
			want := cause
			switch fault {
			case "provider":
				providers = nil
				want = kernel.ErrUnavailable
			case "integration":
				binding.err = cause
			case "missing integration":
				binding.found = false
				want = port.ErrNoIntegration
			case "source":
				provider.err = cause
			case "empty head":
				head = ""
				want = kernel.ErrUnavailable
			case "zero head":
				head = strings.Repeat("0", 40)
				want = kernel.ErrUnavailable
			case "nonhex head":
				head = strings.Repeat("z", 40)
				want = kernel.ErrUnavailable
			}
			s := New(Deps{Providers: providers, Integrations: binding})
			if _, err := s.PrepareTag(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "TEST", vcsapi.Event{Head: head, Tag: "v1"}); !errors.Is(err, want) {
				t.Fatal("tag preparation failure hidden", err, want)
			}
			if fault != "source" && provider.calls != 0 {
				t.Fatal("refused/invalid immutable head performed provider work")
			}
		})
	}
}
