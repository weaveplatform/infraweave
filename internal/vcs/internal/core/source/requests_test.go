package source

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type requestNative struct {
	port.Source
	snapshot         port.RequestSnapshot
	readErr, diffErr error
	resolveErr       error
	resolved         []port.Repository
	refs             []string
	files            []string
	wrongHash        bool
	reads, diffs     int
}

func (p *requestNative) ReadRequest(ctx context.Context, repo port.Repository, number int) (port.RequestSnapshot, error) {
	p.reads++
	if tx.In(ctx) || repo.Integration == nil || number != 12 {
		return port.RequestSnapshot{}, errOffline
	}
	return p.snapshot, p.readErr
}

func (p *requestNative) ResolveRef(ctx context.Context, repo port.Repository, ref string) (vcsapi.Commit, error) {
	if tx.In(ctx) {
		return vcsapi.Commit{}, errOffline
	}
	p.resolved = append(p.resolved, repo)
	p.refs = append(p.refs, ref)
	if p.wrongHash {
		ref = strings.Repeat("f", 40)
	}
	return vcsapi.Commit{Hash: ref, Message: "full\nimmutable message"}, p.resolveErr
}

func (p *requestNative) AffectedFiles(_ context.Context, repo port.Repository, base, head string, _ ...port.DiffMode) ([]string, error) {
	p.diffs++
	if base != p.snapshot.Base || head != p.snapshot.Head || repo.Namespace != "acme" {
		return nil, errOffline
	}
	return p.files, p.diffErr
}

func nativeRequestFixture() (*Service, *requestNative, *actionBindings, vcsapi.Event) {
	target := vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"}
	native := &requestNative{snapshot: port.RequestSnapshot{
		Request: vcsapi.PullRequest{Number: 12, BaseBranch: "main", HeadBranch: "feature", AuthorLogin: "creator"},
		Base:    strings.Repeat("a", 40), Head: strings.Repeat("b", 40), BaseRepository: target,
		HeadRepository: vcsapi.Repository{ID: "9", Namespace: "contributor", Name: "fork"},
	}, files: []string{"main.tf"}}
	binding := &actionBindings{first: port.Integration{ID: kernel.NewULID(), Provider: "GITLAB"}}
	service := New(Deps{Integrations: binding, Providers: map[string]port.Source{"GITLAB": native}})
	event := vcsapi.Event{Type: "pull_request", Repository: target, Head: native.snapshot.Head, Author: "reviewer", PullRequest: &vcsapi.PullRequest{Number: 12}}
	return service, native, binding, event
}

func TestUnitRequestPreparationFreezesNativePeersAndComment(t *testing.T) {
	for _, name := range []string{"request", "comment", "empty comment", "immutable prefix", "same repository"} {
		t.Run(name, func(t *testing.T) {
			s, native, bindings, event := nativeRequestFixture()
			if name == "comment" || name == "empty comment" {
				event.Comment = &vcsapi.Comment{PullRequest: 12, Body: "plan now", Author: "commenter", Head: event.Head}
				event.PullRequest = nil
				if name == "empty comment" {
					event.Comment.Body = ""
				}
			}
			if name == "immutable prefix" {
				event.Head = event.Head[:12]
			}
			if name == "same repository" {
				native.snapshot.HeadRepository = native.snapshot.BaseRepository
				native.snapshot.Request.Fork = true
			}
			facts, err := s.PrepareRequest(t.Context(), kernel.NewAccountID(), bindings.first.ID, "GITLAB", event)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Head.Hash != native.snapshot.Head || facts.Base.Hash != native.snapshot.Base || facts.Head.Branch != "feature" || facts.Base.Branch != "main" ||
				facts.Snapshot.Request.Fork != (name != "same repository") || native.reads != 1 || native.diffs != 1 || bindings.reads != 1 ||
				!reflect.DeepEqual(native.refs, []string{native.snapshot.Head, native.snapshot.Base}) ||
				native.resolved[0].Namespace != native.snapshot.HeadRepository.Namespace || native.resolved[1].Namespace != "acme" {
				t.Fatal("native source, target, branch or immutable commit was substituted")
			}
			if event.Comment == nil {
				if facts.Actor != "reviewer" || facts.Comment != nil {
					t.Fatal("request actor or absent comment changed")
				}
			} else if facts.Actor != "commenter" || facts.Comment == nil || *facts.Comment != event.Comment.Body {
				t.Fatal("comment author or empty/text presence changed")
			}
			native.files[0] = "mutated.tf"
			if facts.Files[0] != "main.tf" {
				t.Fatal("prepared diff aliases the provider's reusable buffer")
			}
		})
	}
}

func TestUnitRequestPreparationRejectsChangedOrIncompleteAuthority(t *testing.T) {
	for _, name := range []string{"unsupported", "missing number", "different comment number", "missing integration", "integration failure", "identity failure", "native failure", "wrong number", "bad head", "bad base", "missing source ID", "missing source namespace", "missing source name", "missing target ID", "different target", "changed delivered head", "changed comment head", "resolve failure", "resolved different commit", "diff failure"} {
		t.Run(name, func(t *testing.T) {
			s, native, bindings, event := nativeRequestFixture()
			want := kernel.ErrUnavailable
			switch name {
			case "unsupported":
				s.providers = nil
			case "missing number":
				event.PullRequest = nil
			case "different comment number":
				event.Comment = &vcsapi.Comment{PullRequest: 13}
			case "missing integration":
				bindings.missing, want = true, port.ErrNoIntegration
			case "integration failure":
				bindings.useErr, want = errOffline, errOffline
			case "identity failure":
				s.identities, want = &identityGuard{err: errOffline}, errOffline
			case "native failure":
				native.readErr, want = errOffline, errOffline
			case "wrong number":
				native.snapshot.Request.Number++
			case "bad head":
				native.snapshot.Head = "refs/heads/feature"
			case "bad base":
				native.snapshot.Base = "main"
			case "missing source ID":
				native.snapshot.HeadRepository.ID = ""
			case "missing source namespace":
				native.snapshot.HeadRepository.Namespace = ""
			case "missing source name":
				native.snapshot.HeadRepository.Name = ""
			case "missing target ID":
				native.snapshot.BaseRepository.ID = ""
			case "different target":
				native.snapshot.BaseRepository.ID = "8"
			case "changed delivered head":
				event.Head = strings.Repeat("c", 40)
			case "changed comment head":
				event.Comment = &vcsapi.Comment{PullRequest: 12, Head: strings.Repeat("c", 40)}
			case "resolve failure":
				native.resolveErr, want = errOffline, errOffline
			case "resolved different commit":
				native.wrongHash = true
			case "diff failure":
				native.diffErr, want = errOffline, errOffline
			}
			facts, err := s.PrepareRequest(t.Context(), kernel.NewAccountID(), bindings.first.ID, "GITLAB", event)
			if !errors.Is(err, want) || !reflect.DeepEqual(facts, port.RequestFacts{}) {
				t.Fatal("invalid authority published policy facts or hid the operational cause", facts, err)
			}
		})
	}
}

func TestUnitRequestPreparationRejectsInheritedTransaction(t *testing.T) {
	s, native, bindings, event := nativeRequestFixture()
	if err := tx.Do(t.Context(), aliasDB{}, tx.Options{}, func(ctx context.Context) error {
		_, err := s.PrepareRequest(ctx, kernel.NewAccountID(), bindings.first.ID, "GITLAB", event)
		if !errors.Is(err, kernel.ErrUnavailable) {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if native.reads != 0 || bindings.reads != 0 {
		t.Fatal("transaction opened credentials or called the provider")
	}
}
