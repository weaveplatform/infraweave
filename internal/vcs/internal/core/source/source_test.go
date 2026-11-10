package source

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var errOffline = errors.New("offline")

// stacks answers Settings from one stack, or err, and records what
// SetTracked sets in tracked.
type stacks struct {
	stacksapi.Stacks
	settings stacksapi.Settings
	err      error
	tracked  *[]stacksapi.Commit
}

func (s stacks) SetTracked(
	_ context.Context,
	_ kernel.AccountID,
	_ kernel.ULID,
	head, commit *stacksapi.Commit,
	setBy string,
) error {
	if *head != *commit || setBy != "infraweave" {
		return errOffline
	}
	*s.tracked = append(*s.tracked, *head)
	return nil
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
	svc := newService(stacks{settings: settings("GIT", &url)}, map[string]port.Source{
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
		svc := newService(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
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
	svc := newService(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
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
	svc := newService(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
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
	missing := newService(stacks{err: stacksapi.ErrNotFound}, nil)
	unread := newService(stacks{settings: settings("GITHUB", nil)}, map[string]port.Source{})
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

// TestUnitTrack checks that a stack tracks its branch's head, set by
// infraweave; that a stack deleted since, or on a provider with no source,
// has nothing to track; that a branch the repository lacks fails for good;
// and that a provider that does not answer fails to be retried.
func TestUnitTrack(t *testing.T) {
	url := "https://git.example/acme/infra.git"
	head := vcsapi.Commit{Hash: "abc", Message: "init", Timestamp: time.Unix(1, 0)}
	acct, id := kernel.NewAccountID(), kernel.NewULID()
	var tracked []stacksapi.Commit
	var got port.Repository
	track := func(st stacks, src port.Source) error {
		st.tracked = &tracked
		return newService(st, map[string]port.Source{"GIT": src}).Track(t.Context(), acct, id)
	}
	if err := track(
		stacks{settings: settings("GIT", &url)},
		source{repo: &got, commit: head},
	); err != nil ||
		len(tracked) != 1 ||
		tracked[0].Hash != "abc" {
		t.Fatalf("Track = %v, tracked %+v", err, tracked)
	}
	for name, st := range map[string]stacks{
		"a deleted stack":  {err: stacksapi.ErrNotFound},
		"another provider": {settings: settings("GITHUB", nil)},
	} {
		if err := track(st, source{repo: &got}); err != nil {
			t.Errorf("Track of %s = %v", name, err)
		}
	}
	if err := track(stacks{err: errOffline}, source{repo: &got}); !errors.Is(err, errOffline) {
		t.Errorf("Track with stacks down = %v", err)
	}
	if err := track(
		stacks{settings: settings("GIT", &url)},
		source{repo: &got, err: port.ErrNotFound},
	); !permanent(err) || !errors.Is(err, port.ErrNotFound) {
		t.Errorf("Track of a missing branch = %v", err)
	}
	if err := track(
		stacks{settings: settings("GIT", &url)},
		source{repo: &got, err: kernel.ErrUnavailable},
	); !errors.Is(err, kernel.ErrUnavailable) || permanent(err) {
		t.Errorf("Track with the provider down = %v", err)
	}
}

// permanent reports whether err is a queue.Permanent result, which the
// queue does not retry.
func permanent(err error) bool {
	return fmt.Sprintf("%T", err) == fmt.Sprintf("%T", queue.Permanent(err))
}

// TestUnitResolver checks that the stacks domain's resolver answers the
// commit as stacks keeps it, and a ref the repository lacks as
// stacksapi.ErrRefNotFound.
func TestUnitResolver(t *testing.T) {
	var got port.Repository
	c := vcsapi.Commit{
		Hash:       "abc",
		Tag:        "v1",
		Message:    "init",
		AuthorName: "Ada",
		Timestamp:  time.Unix(1, 0),
	}
	for _, tc := range []struct {
		err  error
		want error
	}{{nil, nil}, {port.ErrNotFound, stacksapi.ErrRefNotFound}} {
		r := newService(stacks{settings: settings("GIT", nil)}, map[string]port.Source{
			"GIT": source{repo: &got, commit: c, err: tc.err},
		}).Resolver()
		sc, err := r.Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
		if !errors.Is(err, tc.want) || (tc.err == nil && (sc.Hash != "abc" || sc.Tag != "v1")) {
			t.Errorf("Resolve with %v = %+v, %v", tc.err, sc, err)
		}
	}
}

// integrations answers Use with in, found when found is set, or err, and
// records the integration ID it is asked for.
type integrations struct {
	in    port.Integration
	found bool
	err   error
	asked **kernel.ULID
}

func (i integrations) Use(
	_ context.Context,
	_ kernel.AccountID,
	_ string,
	id *kernel.ULID,
) (port.Integration, bool, error) {
	if i.asked != nil {
		*i.asked = id
	}
	return i.in, i.found, i.err
}

// newService is the service over st and providers, whose stacks use no
// integration.
func newService(st stacksapi.Stacks, providers map[string]port.Source) *Service {
	return New(st, providers, integrations{})
}

// TestUnitIntegration checks that the Source reaches the repository
// through the integration the stack uses, its own when it names one; that
// a failed read of it fails the resolve; and that a detached stack is
// refused as invalid.
func TestUnitIntegration(t *testing.T) {
	id := kernel.NewULID()
	set := settings("GITLAB", nil)
	set.VCSIntegration = &id
	use := port.Integration{Provider: "GITLAB", Credential: []byte("token")}
	var got port.Repository
	var asked *kernel.ULID
	svc := New(stacks{settings: set}, map[string]port.Source{
		"GITLAB": source{repo: &got, commit: vcsapi.Commit{Hash: "abc"}},
	}, integrations{in: use, found: true, asked: &asked})
	if _, err := svc.Resolve(
		t.Context(),
		kernel.NewAccountID(),
		kernel.NewULID(),
		"main",
	); err != nil {
		t.Fatal(err)
	}
	if got.Integration == nil || string(got.Integration.Credential) != "token" ||
		asked == nil || *asked != id {
		t.Errorf("repository = %+v, asked for %v", got, asked)
	}
	failed := New(stacks{settings: set}, map[string]port.Source{"GITLAB": source{repo: &got}},
		integrations{err: errOffline})
	if _, err := failed.Resolve(
		t.Context(),
		kernel.NewAccountID(),
		kernel.NewULID(),
		"main",
	); !errors.Is(
		err,
		errOffline,
	) {
		t.Errorf("Resolve with a failed integration = %v", err)
	}
	set.VCSDetached = true
	detached := New(stacks{settings: set}, map[string]port.Source{"GITLAB": source{repo: &got}},
		integrations{})
	_, err := detached.Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
	if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
		t.Errorf("Resolve of a detached stack = %v", err)
	}
}
