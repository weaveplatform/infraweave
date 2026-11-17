package rawgit

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/source"
)

type runFileStack struct {
	stacksapi.Stacks
	url string
}

func (s runFileStack) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return stacksapi.Settings{Provider: "GIT", RepositoryURL: &s.url, Repository: "infra"}, nil
}

type noFileIntegration struct{}

func (noFileIntegration) Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	return port.Integration{}, false, nil
}

func TestUnitRunFileThroughRawGit(t *testing.T) {
	repo := newRepository(t)
	server, provider := serve(t, gitServer{repos: map[string]*memory.Storage{"infra": repo.store}}.routes())
	runs := source.Runs{Service: source.New(source.Deps{
		Stacks: runFileStack{url: server.URL + "/infra.git"}, Integrations: noFileIntegration{},
		Providers: map[string]port.Source{"GIT": provider},
	})}
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	data, found, err := runs.File(t.Context(), acct, stack, repo.head, ".infraweave/config.yml")
	if err != nil || !found || string(data) != "version: 1\n" {
		t.Fatalf("committed file: %q %t %v", data, found, err)
	}
	if _, found, err := runs.File(t.Context(), acct, stack, repo.base, ".infraweave/config.yml"); err != nil || found {
		t.Fatalf("read a later commit's file from the base: %t %v", found, err)
	}
	if _, _, err := runs.File(t.Context(), acct, stack, repo.huge, "big.bin"); !errors.Is(err, runsapi.ErrFileTooLarge) {
		t.Fatalf("large raw Git blob: %v", err)
	}
}
