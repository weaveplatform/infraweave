package pullrequest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/push"
)

type requestHeadBindings struct {
	Integrations
	account kernel.AccountID
	id      kernel.ULID
	calls   int
	err     error
}

func (b *requestHeadBindings) Binding(_ context.Context, account kernel.AccountID, provider string, id *kernel.ULID) (port.Integration, bool, error) {
	if id == nil || *id != b.id || account != b.account || provider != "BITBUCKET_CLOUD" {
		return port.Integration{}, false, nil
	}
	return port.Integration{ID: b.id, Provider: provider}, true, nil
}

func (b *requestHeadBindings) Use(ctx context.Context, account kernel.AccountID, provider string, id *kernel.ULID) (port.Integration, bool, error) {
	b.calls++
	if tx.In(ctx) {
		return port.Integration{}, false, errors.New("credential lookup inside transaction")
	}
	if b.err != nil {
		return port.Integration{}, false, b.err
	}
	if id == nil || *id != b.id || account != b.account || provider != "BITBUCKET_CLOUD" {
		return port.Integration{}, false, nil
	}
	return port.Integration{ID: b.id, Provider: provider, Credential: []byte("owned-credential")}, true, nil
}

type requestHeads struct {
	t           *testing.T
	integration kernel.ULID
	source      vcsapi.Repository
	head        string
	calls       int
	err         error
}

func (h *requestHeads) ResolveRequestHead(ctx context.Context, in port.Integration, source vcsapi.Repository, prefix string) (string, error) {
	h.t.Helper()
	h.calls++
	if tx.In(ctx) {
		h.t.Fatal("native request head resolved inside transaction")
	}
	if _, ok := ctx.Deadline(); !ok {
		h.t.Fatal("native request head has no delivery deadline")
	}
	if in.ID != h.integration || in.Provider != "BITBUCKET_CLOUD" || string(in.Credential) != "owned-credential" || source != h.source || prefix != "d3022fc0ca3d" {
		h.t.Fatal("head resolver lost account-bound integration, immutable prefix or fork identity")
	}
	return h.head, h.err
}

type requestNoRuns struct{ calls int }

func (c *requestNoRuns) Create(context.Context, runsapi.CreateRequest) (runsapi.CreatedRun, error) {
	c.calls++
	return runsapi.CreatedRun{}, kernel.ErrUnavailable
}

func requestHeadEvent() vcsapi.Event {
	e := requestEvent()
	e.Head = "d3022fc0ca3d"
	e.PullRequest.AuthorLogin = "creator"
	e.PullRequest.HeadRepository = &vcsapi.Repository{ID: "{00000000-0000-4000-8000-000000000009}", Namespace: "contributor", Name: "different-fork"}
	e.PullRequest.HeadOwner = "contributor"
	e.PullRequest.Fork = true
	return e
}

func TestUnitRequestHeadPreparationKeepsImmutableSourceAndNoRuns(t *testing.T) {
	for _, action := range []string{"opened", "edited"} {
		t.Run(action, func(t *testing.T) {
			d, e := requestDelivery(), requestHeadEvent()
			d.Provider = "BITBUCKET_CLOUD"
			e.Action = action
			store := &memoryStore{}
			bindings := &requestHeadBindings{account: d.Account, id: d.Integration}
			head := "d3022fc0ca3d" + strings.Repeat("a", 28)
			resolver := &requestHeads{t: t, integration: d.Integration, source: *e.PullRequest.HeadRepository, head: head}
			runs := &requestNoRuns{}
			s := New(Deps{Store: store, Integrations: bindings, Heads: map[string]Heads{d.Provider: resolver}, Pushes: push.New(push.Deps{Creator: runs})})
			plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{e})
			if err != nil || plan == nil {
				t.Fatal(err)
			}
			if len(store.rows) != 0 || resolver.calls != 1 || bindings.calls != 1 || runs.calls != 0 {
				t.Fatal("preparation persisted, created a run or lost native bounds")
			}
			if err := plan.Commit(t.Context()); !errors.Is(err, ErrNoTransaction) {
				t.Fatal("unfenced request snapshot committed")
			}
			if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
				t.Fatal(err)
			}
			if len(store.rows) != 1 || store.rows[0].HeadSHA != head || store.rows[0].Account != d.Account || store.rows[0].Integration != d.Integration ||
				store.rows[0].Number != "17" ||
				store.rows[0].Namespace != "acme" ||
				store.rows[0].Repository != "infra" ||
				store.rows[0].HeadBranch != "feature" ||
				runs.calls != 0 {
				t.Fatal("request snapshot substituted destination/source, retained a prefix, or created a run")
			}
		})
	}
}

func TestUnitRequestHeadRefusalsCannotPublishSnapshotsOrRuns(t *testing.T) {
	for _, name := range []string{"missing source", "missing source ID", "missing source namespace", "missing source name", "mutable ref", "short ref", "zero ref", "nonhex ref", "foreign account", "foreign integration", "integration failure", "native failure", "native partial", "native invalid", "native mismatch", "unsupported provider", "missing resolver"} {
		t.Run(name, func(t *testing.T) {
			d, e := requestDelivery(), requestHeadEvent()
			d.Provider = "BITBUCKET_CLOUD"
			store := &memoryStore{}
			bindings := &requestHeadBindings{account: d.Account, id: d.Integration}
			resolver := &requestHeads{t: t, integration: d.Integration, source: *e.PullRequest.HeadRepository, head: "d3022fc0ca3d" + strings.Repeat("a", 28)}
			providers := map[string]Heads{d.Provider: resolver}
			cause := errors.New("native preparation temporarily unavailable")
			wantErr := ErrHead
			wantBinding, wantNative := 0, 0
			switch name {
			case "missing source":
				e.PullRequest.HeadRepository = nil
			case "missing source ID":
				e.PullRequest.HeadRepository.ID = ""
			case "missing source namespace":
				e.PullRequest.HeadRepository.Namespace = ""
			case "missing source name":
				e.PullRequest.HeadRepository.Name = ""
			case "mutable ref":
				e.Head = "refs/heads/feature"
			case "short ref":
				e.Head = "d3022f"
			case "zero ref":
				e.Head = strings.Repeat("0", 12)
			case "nonhex ref":
				e.Head = "not-a-commit"
			case "foreign account":
				d.Account = kernel.NewAccountID()
				wantBinding = 1
			case "foreign integration":
				d.Integration = kernel.NewULID()
				wantBinding = 1
			case "integration failure":
				bindings.err = cause
				wantErr = cause
				wantBinding = 1
			case "native failure":
				resolver.err = cause
				wantErr = cause
				wantBinding, wantNative = 1, 1
			case "native partial":
				resolver.head = e.Head
				wantBinding, wantNative = 1, 1
			case "native invalid":
				resolver.head = strings.Repeat("z", 40)
				wantBinding, wantNative = 1, 1
			case "native mismatch":
				resolver.head = strings.Repeat("a", 40)
				wantBinding, wantNative = 1, 1
			case "unsupported provider":
				d.Provider = "GITLAB"
			case "missing resolver":
				providers = nil
			}
			runs := &requestNoRuns{}
			s := New(Deps{Store: store, Integrations: bindings, Heads: providers, Pushes: push.New(push.Deps{Creator: runs})})
			plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{e})
			if !errors.Is(err, wantErr) || plan != nil || len(store.rows) != 0 || runs.calls != 0 || resolver.calls != wantNative || bindings.calls != wantBinding {
				t.Fatal("failed request preparation published work or lost scoped failure", err)
			}
		})
	}
}

func TestUnitRequestHeadPreservesCompleteAndNonOpenPaths(t *testing.T) {
	for _, name := range []string{"sha1", "sha256", "closed missing head", "merged missing head", "closed prefix", "ordinary comment"} {
		t.Run(name, func(t *testing.T) {
			d, e := requestDelivery(), requestHeadEvent()
			d.Provider = "BITBUCKET_CLOUD"
			store := &memoryStore{}
			bindings := &requestHeadBindings{account: d.Account, id: d.Integration}
			resolver := &requestHeads{t: t}
			e.PullRequest.HeadRepository = nil
			wantRows := 1
			switch name {
			case "sha1":
				e.Head = strings.Repeat("A", 40)
			case "sha256":
				e.Head = strings.Repeat("B", 64)
			case "closed missing head":
				e.Head = ""
				e.PullRequest.State = Closed
				e.Action = "closed"
			case "merged missing head":
				e.Head = ""
				e.PullRequest.State = Merged
				e.Action = "merged"
			case "closed prefix":
				e.PullRequest.State = Closed
				e.Action = "closed"
			case "ordinary comment":
				e.Type = "comment"
				e.PullRequest = nil
				e.Comment = &vcsapi.Comment{Body: "ordinary comment", PullRequest: 17, Head: e.Head}
				wantRows = 0
			}
			runs := &requestNoRuns{}
			s := New(Deps{Store: store, Integrations: bindings, Heads: map[string]Heads{d.Provider: resolver}, Pushes: push.New(push.Deps{Creator: runs})})
			plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{e})
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
				t.Fatal(err)
			}
			if len(store.rows) != wantRows || resolver.calls != 0 || bindings.calls != 0 || runs.calls != 0 {
				t.Fatal("complete/non-open/comment path acquired a native dependency or created a run")
			}
			if wantRows == 1 && store.rows[0].HeadSHA != e.Head {
				t.Fatal("existing complete/non-open head changed")
			}
		})
	}
}

func TestUnitRequestHeadRefusesInheritedTransactionBeforeNativeReads(t *testing.T) {
	d, e := requestDelivery(), requestHeadEvent()
	d.Provider = "BITBUCKET_CLOUD"
	store := &memoryStore{}
	bindings := &requestHeadBindings{account: d.Account, id: d.Integration}
	resolver := &requestHeads{t: t, integration: d.Integration, source: *e.PullRequest.HeadRepository, head: e.Head + strings.Repeat("a", 28)}
	s := New(Deps{Store: store, Integrations: bindings, Heads: map[string]Heads{d.Provider: resolver}})
	if err := tx.Do(t.Context(), planDB{}, tx.Options{}, func(ctx context.Context) error {
		plan, err := s.Prepare(ctx, d, []vcsapi.Event{e})
		if !errors.Is(err, ErrHead) || plan != nil {
			t.Fatal("native head read admitted inside transaction")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bindings.calls != 0 || resolver.calls != 0 || len(store.rows) != 0 {
		t.Fatal("transaction refusal opened credentials/native lookup")
	}
	plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{e})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 1 || resolver.calls != 1 {
		t.Fatal("original snapshot did not recover outside transaction")
	}
}

func TestUnitExpandedRequestHeadMatchesExistingLowercaseRun(t *testing.T) {
	d, e := requestDelivery(), requestHeadEvent()
	d.Provider = "BITBUCKET_CLOUD"
	full := e.Head + strings.Repeat("a", 28)
	store := &memoryStore{heads: map[kernel.ULID][]string{}}
	binding := &requestHeadBindings{account: d.Account, id: d.Integration}
	resolver := &requestHeads{t: t, integration: d.Integration, source: *e.PullRequest.HeadRepository, head: strings.ToUpper(full)}
	creator := &requestNoRuns{}
	run := runsapi.CommitRun{ID: kernel.NewULID(), CommitSHA: full, CreatedAt: d.ReceivedAt}
	existing := &commitRuns{rows: []runsapi.CommitRun{run}}
	s := New(Deps{Store: store, Integrations: binding, Heads: map[string]Heads{d.Provider: resolver}, Runs: existing, Pushes: push.New(push.Deps{Creator: creator})})
	plan, err := s.Prepare(t.Context(), d, []vcsapi.Event{e})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(t.Context(), planDB{}, tx.Options{}, plan.Commit); err != nil {
		t.Fatal(err)
	}
	if len(store.rows) != 1 {
		t.Fatal("request was not retained")
	}
	stored := store.rows[0]
	store.heads[stored.ID] = []string{stored.HeadSHA}
	stack := stacksapi.Settings{
		Stack:          stacksapi.Stack{ID: kernel.NewULID(), Account: d.Account},
		Provider:       d.Provider,
		VCSIntegration: &d.Integration,
		Namespace:      e.Repository.Namespace,
		Repository:     e.Repository.Name,
		Branch:         e.PullRequest.BaseBranch,
	}
	views, err := s.PullRequests(t.Context(), d.Account, []stacksapi.Settings{stack})
	if err != nil {
		t.Fatal(err)
	}
	if stored.HeadSHA != full || len(views[stack.ID]) != 1 || views[stack.ID][0].HeadSHA != full || len(views[stack.ID][0].Runs) != 1 || views[stack.ID][0].Runs[0] != run.ID {
		t.Fatal("expanded head lost canonical storage or the existing pushed-run association")
	}
	if creator.calls != 0 || existing.calls != 1 || existing.account != d.Account || existing.stack != stack.ID || binding.calls != 1 {
		t.Fatal("request read created a run, reopened credentials or crossed account/stack scope")
	}
}
