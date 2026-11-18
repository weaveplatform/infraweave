package pullrequest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type memoryStore struct {
	rows          []Request
	heads         map[kernel.ULID][]string
	author        string
	errAt         string
	cause         error
	pages         int
	prunes        []int
	pruneAccounts []*kernel.AccountID
	pruneBefore   []*time.Time
}

func (m *memoryStore) Upsert(_ context.Context, r Request) error {
	if m.errAt == "upsert" {
		return m.cause
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *memoryStore) Author(context.Context, kernel.AccountID, kernel.ULID, string, string) (string, error) {
	if m.errAt == "author" {
		return "", m.cause
	}
	return m.author, nil
}

func (m *memoryStore) Candidates(_ context.Context, _ kernel.AccountID, _ kernel.ULID, _, _, _ string, after *Cursor, limit int) ([]Request, error) {
	m.pages++
	if m.errAt == "candidates" {
		return nil, m.cause
	}
	start := 0
	if after != nil {
		start = slices.IndexFunc(m.rows, func(r Request) bool { return r.ID == after.ID }) + 1
	}
	return m.rows[start:min(start+limit, len(m.rows))], nil
}

func (m *memoryStore) Heads(_ context.Context, _ kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID][]string, error) {
	if m.errAt == "heads" {
		return nil, m.cause
	}
	out := map[kernel.ULID][]string{}
	for _, id := range ids {
		out[id] = slices.Clone(m.heads[id])
	}
	return out, nil
}

func (m *memoryStore) Prune(_ context.Context, acct *kernel.AccountID, before *time.Time, _ int) (int, error) {
	m.pruneAccounts = append(m.pruneAccounts, acct)
	m.pruneBefore = append(m.pruneBefore, before)
	if m.errAt == "prune" {
		return 0, m.cause
	}
	n := m.prunes[0]
	m.prunes = m.prunes[1:]
	return n, nil
}

type bindings struct {
	found    bool
	err      error
	used     int
	named    *kernel.ULID
	provider string
}

func (b *bindings) Binding(_ context.Context, _ kernel.AccountID, provider string, id *kernel.ULID) (port.Integration, bool, error) {
	b.named, b.provider = id, provider
	return port.Integration{ID: kernel.NewULID()}, b.found, b.err
}

func (b *bindings) Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	b.used++
	return port.Integration{}, b.found, b.err
}

type commitRuns struct {
	rows    []runsapi.CommitRun
	err     error
	calls   int
	account kernel.AccountID
	stack   kernel.ULID
}

func (r *commitRuns) OnCommits(_ context.Context, acct kernel.AccountID, stack kernel.ULID, hashes []string) ([]runsapi.CommitRun, error) {
	r.calls++
	r.account, r.stack = acct, stack
	return slices.DeleteFunc(slices.Clone(r.rows), func(row runsapi.CommitRun) bool { return !slices.Contains(hashes, row.CommitSHA) }), r.err
}

func TestUnitMembershipBeforeRequestCap(t *testing.T) {
	t.Parallel()
	store := &memoryStore{heads: map[kernel.ULID][]string{}}
	runs := &commitRuns{}
	now := time.Now().UTC()
	for i := range 160 {
		r := Request{ID: kernel.NewULID(), Number: strconv.Itoa(i), HeadSHA: fmt.Sprint(i), UpdatedAt: now.Add(-time.Duration(i) * time.Second)}
		store.rows = append(store.rows, r)
		store.heads[r.ID] = []string{r.HeadSHA}
		if i >= 100 {
			runs.rows = append(runs.rows, runsapi.CommitRun{ID: kernel.NewULID(), CommitSHA: r.HeadSHA, CreatedAt: now})
		}
	}
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	binding := &bindings{found: true}
	s := New(Deps{Store: store, Integrations: binding, Runs: runs})
	views, err := s.PullRequests(t.Context(), acct, []stacksapi.Settings{{Stack: stacksapi.Stack{ID: stack}, Provider: "GITLAB"}})
	if err != nil || len(views[stack]) != 50 || views[stack][0].ID != "100" || views[stack][49].ID != "149" || store.pages != 2 || runs.calls != 2 {
		t.Fatalf("cap preceded membership or lost page order: count=%d pages=%d reads=%d err=%v", len(views[stack]), store.pages, runs.calls, err)
	}
	if runs.account != acct || runs.stack != stack || binding.used != 0 {
		t.Fatal("read opened credentials or crossed run scope")
	}
}

func TestUnitHistoricalAndSharedHeads(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	first := Request{
		ID:            kernel.NewULID(),
		Number:        "17",
		AuthorLogin:   "creator",
		Title:         "changes",
		URL:           "https://example.com/pr/17",
		HeadBranch:    "feature",
		HeadBranchURL: "https://example.com/feature",
		HeadSHA:       "current",
		Draft:         true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	second := first
	second.ID = kernel.NewULID()
	second.Number = "18"
	store := &memoryStore{rows: []Request{first, second}, heads: map[kernel.ULID][]string{first.ID: {"older", "current"}, second.ID: {"current", "older"}}}
	older := runsapi.CommitRun{ID: kernel.NewULID(), CommitSHA: "older", CreatedAt: now.Add(time.Hour)}
	current := runsapi.CommitRun{ID: kernel.NewULID(), CommitSHA: "current", CreatedAt: now}
	later := current
	later.ID = kernel.NewULID()
	runs := &commitRuns{rows: []runsapi.CommitRun{older, current, later}}
	stack := kernel.NewULID()
	s := New(Deps{Store: store, Integrations: &bindings{found: true}, Runs: runs})
	result, err := s.PullRequests(t.Context(), kernel.NewAccountID(), []stacksapi.Settings{{Stack: stacksapi.Stack{ID: stack}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range result[stack] {
		if !reflect.DeepEqual(pr.Runs, []kernel.ULID{later.ID, current.ID, older.ID}) || !pr.Draft || pr.AuthorLogin != "creator" || pr.Title != "changes" ||
			pr.HeadBranchURL != first.HeadBranchURL ||
			!pr.CreatedAt.Equal(now) {
			t.Fatalf("lost metadata/history/order: %+v", pr)
		}
	}
	if len(result[stack]) != 2 {
		t.Fatal("a shared run was assigned to only one request")
	}
	// The old head alone still makes both requests eligible.
	runs.rows = []runsapi.CommitRun{older}
	result, err = s.PullRequests(t.Context(), kernel.NewAccountID(), []stacksapi.Settings{{Stack: stacksapi.Stack{ID: stack}}})
	if err != nil || len(result[stack]) != 2 || len(result[stack][0].Runs) != 1 {
		t.Fatal("old head membership was discarded", result, err)
	}
}

func TestUnitRequestReadScopeAndFailures(t *testing.T) {
	for _, where := range []string{"binding", "candidates", "heads", "runs", "unbound runs", "no heads", "no binding", "detached", "empty page", "no stacks"} {
		t.Run(where, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("unavailable")
			row := Request{ID: kernel.NewULID(), HeadSHA: "sha"}
			store := &memoryStore{rows: []Request{row}, heads: map[kernel.ULID][]string{row.ID: {"sha"}}, cause: cause, errAt: where}
			binding := &bindings{found: true}
			runs := &commitRuns{}
			if where == "binding" {
				binding.err = cause
			}
			if where == "runs" {
				runs.err = cause
			}
			if where == "no binding" {
				binding.found = false
			}
			if where == "no heads" {
				store.heads = nil
			}
			if where == "empty page" {
				store.rows = nil
			}
			id := kernel.NewULID()
			settings := []stacksapi.Settings{{Stack: stacksapi.Stack{ID: id}, Provider: "GITHUB", VCSIntegration: &id, VCSDetached: where == "detached"}}
			if where == "no stacks" {
				settings = nil
			}
			s := New(Deps{Store: store, Integrations: binding, Runs: runs})
			if where == "unbound runs" {
				s.d.Runs = nil
				cause = kernel.ErrUnavailable
			}
			_, err := s.PullRequests(t.Context(), kernel.NewAccountID(), settings)
			want := slices.Contains([]string{"binding", "candidates", "heads", "runs", "unbound runs"}, where)
			if want && !errors.Is(err, cause) || !want && err != nil {
				t.Fatal("wrong scoped read failure", err)
			}
			if where != "detached" && where != "no stacks" && (binding.named == nil || *binding.named != id || binding.provider != "GITHUB") {
				t.Fatal("explicit binding/provider lost")
			}
			if binding.used != 0 {
				t.Fatal("read opened integration credentials")
			}
		})
	}
}
