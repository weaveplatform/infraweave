package source

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errOffline = errors.New("offline")

// stacks answers Settings from one stack, or err.
type stacks struct {
	stacksapi.Stacks
	settings stacksapi.Settings
	err      error
}

func (s stacks) Settings(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (stacksapi.Settings, error) {
	return s.settings, s.err
}

// source records the repository it is asked about and answers ResolveRef
// with commit, or err.
type source struct {
	port.Source
	repo   *port.Repository
	commit vcsapi.Commit
	err    error
}

func (s source) ResolveRef(
	_ context.Context,
	repo port.Repository,
	_ string,
) (vcsapi.Commit, error) {
	*s.repo = repo
	return s.commit, s.err
}

func (s source) SourceURL(repo port.Repository, commit string) string {
	*s.repo = repo
	return repo.URL + "#" + commit
}

func settings(provider string, url *string) stacksapi.Settings {
	return stacksapi.Settings{
		Provider:      provider,
		Namespace:     "acme",
		Repository:    "infra",
		RepositoryURL: url,
	}
}

// TestUnitResolve checks that a ref resolves through the Source of the
// stack's provider, over the stack's repository, and that a ref the
// Source lacks is vcsapi.ErrRefNotFound, a repository URL it refuses is
// invalid at /repositoryURL, and another failure keeps its cause.
func TestUnitResolve(t *testing.T) {
	url := "https://git.example/acme/infra.git"
	want := vcsapi.Commit{Hash: "abc", Message: "init", Timestamp: time.Unix(1, 0)}
	var got port.Repository
	svc := New(stacks{settings: settings("GIT", &url)}, map[string]port.Source{
		"GIT": source{repo: &got, commit: want},
	})
	c, err := svc.Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
	if err != nil || c != want {
		t.Fatalf("Resolve = %+v, %v", c, err)
	}
	if got != (port.Repository{Namespace: "acme", Name: "infra", URL: url}) {
		t.Errorf("repository = %+v", got)
	}
	for _, tc := range []struct {
		err  error
		want error
	}{
		{port.ErrNotFound, vcsapi.ErrRefNotFound},
		{errOffline, errOffline},
		{kernel.ErrUnavailable, kernel.ErrUnavailable},
	} {
		svc := New(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
			"GIT": source{repo: &got, err: tc.err},
		})
		if _, err := svc.Resolve(
			t.Context(),
			kernel.NewAccountID(),
			kernel.NewULID(),
			"v1",
		); !errors.Is(
			err,
			tc.want,
		) {
			t.Errorf("Resolve with %v = %v, want %v", tc.err, err, tc.want)
		}
	}
}

// TestUnitResolveRepositoryURL checks that a repository URL the Source
// refuses is invalid at /repositoryURL.
func TestUnitResolveRepositoryURL(t *testing.T) {
	var got port.Repository
	svc := New(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
		"GIT": source{repo: &got, err: port.ErrRepositoryURL},
	})
	_, err := svc.Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
	inv, ok := errors.AsType[*kernel.Invalid](err)
	if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/repositoryURL" ||
		inv.Errors[0].Reason != kernel.ReasonRule {
		t.Errorf("Resolve = %v", err)
	}
}

// TestUnitSourceURL checks that the source URL is the one the stack's
// provider names for the commit, and that a repository without a URL
// reaches the Source without one.
func TestUnitSourceURL(t *testing.T) {
	var got port.Repository
	svc := New(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
		"GIT": source{repo: &got},
	})
	u, err := svc.SourceURL(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "abc")
	if err != nil || u != "#abc" || got.URL != "" {
		t.Errorf("SourceURL = %q, %v; repository %+v", u, err, got)
	}
}

// TestUnitSourceRefused checks that a stack the account lacks keeps
// stacksapi.ErrNotFound, and that a provider without a Source is refused
// as invalid at /provider, by both methods.
func TestUnitSourceRefused(t *testing.T) {
	missing := New(stacks{err: stacksapi.ErrNotFound}, nil)
	unread := New(stacks{settings: settings("GITHUB", nil)}, map[string]port.Source{})
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	for name, call := range map[string]func(*Service) error{
		"Resolve": func(s *Service) error {
			_, err := s.Resolve(t.Context(), acct, id, "main")
			return err
		},
		"SourceURL": func(s *Service) error {
			_, err := s.SourceURL(t.Context(), acct, id, "abc")
			return err
		},
	} {
		if err := call(missing); !errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("%s of a missing stack = %v", name, err)
		}
		inv, ok := errors.AsType[*kernel.Invalid](call(unread))
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/provider" ||
			inv.Errors[0].Reason != kernel.ReasonRule {
			t.Errorf("%s of an unread provider = %+v", name, inv)
		}
	}
}
