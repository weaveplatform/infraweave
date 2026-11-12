package pg_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/trigger"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// triggerer is a trigger service of w's, with w's sources bound when
// bound is set.
func (w *world) triggerer(bound bool) *trigger.Service {
	s := trigger.New(trigger.Deps{
		DB: w.db, Stacks: w.stacks, Lifecycle: w.lc, Views: w.reader(w.access()),
	})
	if bound {
		s.BindSources(w.sources)
	}
	return s
}

// alice is a member of w's account.
func (w *world) alice() kernel.Principal {
	return kernel.Principal{
		Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: w.acct, Login: "alice",
	}
}

// TestAccTrigger triggers runs on the stack's tracked commit, else its
// branch head, and on a named commit; answers a second trigger of one
// stack, type and commit with the first run; stores the runtime
// configuration and force-apply mode; and refuses another type, an
// unknown commit, a stack with no commit, an unknown stack, a named
// commit while no source is bound and a disabled stack, even for a run it
// holds.
func TestAccTrigger(t *testing.T) {
	w := newWorld(t)
	s := w.triggerer(true)
	head := stacksapi.Commit{Hash: "head", Message: "Head", Timestamp: w.now.Add(-time.Hour)}
	tracked := stacksapi.Commit{Hash: "tracked", Message: "Pinned", AuthorName: "Bob"}
	w.sources.commits = map[string]stacksapi.Commit{"named": {Hash: "named", Message: "Named"}}
	st := w.stacks.settings[w.stack]
	st.Branch = "main"
	w.stacks.settings[w.stack] = st

	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{}); !errors.Is(err,
		trigger.ErrNoCommit) {
		t.Errorf("a stack with no commit = %v, want ErrNoCommit", err)
	}
	st.TrackedBranchHead = &head
	w.stacks.settings[w.stack] = st
	v, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{})
	if err != nil || v.Run.Commit.Hash != "head" || v.Run.Type != kernel.RunTypeTracked ||
		v.Run.Commit.Branch != "main" || v.Stack.Slug != "network" || len(v.History) != 1 ||
		v.History[0].State != kernel.RunStateQueued || *v.History[0].Username != "alice" {
		t.Fatalf("on the branch head: %+v, %v", v, err)
	}
	st.TrackedCommit = &tracked
	w.stacks.settings[w.stack] = st
	rc := json.RawMessage(`{"runnerImage":"custom"}`)
	v, err = s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed, RuntimeConfig: rc, ForceApply: "SINGLE",
	})
	if err != nil || v.Run.Commit.Hash != "tracked" || v.Run.Type != kernel.RunTypeProposed {
		t.Fatalf("on the tracked commit: %+v, %v", v.Run, err)
	}
	var (
		config []byte
		force  string
	)
	if err := w.db.QueryRow(t.Context(), `SELECT runtime_config, force_apply_mode FROM runs
		WHERE account_id = $1 AND id = $2`, w.acct, v.Run.ID).Scan(&config, &force); err != nil ||
		string(config) != `{"runnerImage": "custom"}` || force != "SINGLE" {
		t.Errorf("stored runtime config %s and force-apply %q, %v", config, force, err)
	}
	again, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		Type: kernel.RunTypeProposed,
	})
	if err != nil || again.Run.ID != v.Run.ID || string(again.Run.RuntimeConfig) == "" ||
		again.Run.Commit.Branch != "main" {
		t.Errorf("a second trigger = %+v, %v; want %s with its configuration and branch",
			again.Run, err, v.Run.ID)
	}
	named, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{CommitSHA: "named"})
	if err != nil || named.Run.Commit.Hash != "named" || named.Run.Commit.Message != "Named" ||
		named.Run.Commit.Branch != "main" {
		t.Errorf("on a named commit: %+v, %v", named.Run.Commit, err)
	}

	for name, c := range map[string]struct {
		s       *trigger.Service
		slug    string
		in      trigger.Input
		pointer string
		is      error
	}{
		"a task":          {s, "network", trigger.Input{Type: kernel.RunTypeTask}, "/runType", nil},
		"an unknown SHA":  {s, "network", trigger.Input{CommitSHA: "gone"}, "/commitSha", nil},
		"an unknown slug": {s, "gone", trigger.Input{}, "", stacksapi.ErrNotFound},
		"no sources": {
			w.triggerer(false), "network",
			trigger.Input{CommitSHA: "named"},
			"",
			kernel.ErrUnavailable,
		},
	} {
		_, err := c.s.Trigger(t.Context(), w.alice(), c.slug, c.in)
		if c.is != nil {
			if !errors.Is(err, c.is) {
				t.Errorf("%s = %v, want %v", name, err, c.is)
			}
			continue
		}
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != c.pointer {
			t.Errorf("%s = %v, want Invalid at %s", name, err, c.pointer)
		}
	}

	w.stacks.err["Settings"] = errInjected
	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{}); !errors.Is(err,
		errInjected) {
		t.Errorf("a failed settings read = %v", err)
	}
	delete(w.stacks.err, "Settings")
	st.Disabled = true
	w.stacks.settings[w.stack] = st
	if _, err := s.Trigger(t.Context(), w.alice(), "network", trigger.Input{
		CommitSHA: "named",
	}); !errors.Is(err, lifecycle.ErrDisabled) {
		t.Errorf("a disabled stack = %v, want ErrDisabled", err)
	}
}
