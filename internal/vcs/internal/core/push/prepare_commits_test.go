package push

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/intake"
)

func TestUnitPreparedCompleteNativeCommitsRetainRevertedPaths(t *testing.T) {
	account, integration := kernel.NewAccountID(), kernel.NewULID()
	files := []string{"main.tf"}
	stacks := &repositoryStacks{rows: []stacksapi.Stack{{ID: kernel.NewULID()}}}
	s := New(Deps{Stacks: stacks, Sources: &pushSource{t: t}, Integrations: eventUse{use: stacksapi.Use{Integration: integration, Provider: "GITLAB", Default: true}, found: true}})
	event := vcsapi.Event{
		Type:       "push",
		Branch:     "main",
		Head:       strings.Repeat("a", 40),
		Repository: vcsapi.Repository{Namespace: "acme", Name: "infra"},
		Commits:    &vcsapi.CommitCollection{Source: "commits", Items: []vcsapi.PushCommit{{Hash: strings.Repeat("a", 40), At: time.Now(), Files: &files}}},
	}
	p, err := s.Prepare(
		t.Context(),
		intake.Delivery{Account: account, ID: kernel.NewULID(), Integration: integration, Provider: "GITLAB", Deadline: time.Now().Add(time.Minute)},
		[]vcsapi.Event{event},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared := p.(*plan).events
	if len(prepared) != 1 || !slices.Equal(prepared[0].Files, []string{"main.tf"}) {
		t.Fatal("complete native commit paths disappeared from overall prepared push", prepared)
	}
}
