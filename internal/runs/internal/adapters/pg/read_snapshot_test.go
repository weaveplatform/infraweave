package pg_test

import (
	"context"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policiesapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// interleavedRead commits the real writer immediately after a chosen read,
// before the public projection reads the remaining tables. No timing or sleep
// decides which side of the commit each query observes.
type interleavedRead struct {
	read.Store
	boundary string
	commit   func()
}

func (s *interleavedRead) after(boundary string) {
	if s.boundary == boundary && s.commit != nil {
		commit := s.commit
		s.commit = nil
		commit()
	}
}

func (s *interleavedRead) Get(ctx context.Context, account kernel.AccountID, id kernel.ULID) (lifecycle.Run, error) {
	run, err := s.Store.Get(ctx, account, id)
	if err == nil {
		s.after("run")
	}
	return run, err
}

func (s *interleavedRead) List(ctx context.Context, account kernel.AccountID, stack kernel.ULID, types []kernel.RunType, query read.Query) ([]lifecycle.Run, error) {
	runs, err := s.Store.List(ctx, account, stack, types, query)
	if err == nil {
		s.after("run")
	}
	return runs, err
}

func (s *interleavedRead) Histories(ctx context.Context, account kernel.AccountID, runs []kernel.ULID) (map[kernel.ULID][]lifecycle.Step, error) {
	histories, err := s.Store.Histories(ctx, account, runs)
	if err == nil {
		s.after("history")
	}
	return histories, err
}

type deletedReadPolicies struct{}

func (deletedReadPolicies) Identities(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]policiesapi.PolicyIdentity, error) {
	return map[kernel.ULID]policiesapi.PolicyIdentity{}, nil
}

// A public read may finish before or after a transition commits, but must not
// join the earlier run state to the later history, receipts, flags and guards.
// This reproduces the worker PLAN report race observed in the real CI lab.
func TestAccPublicRunReadKeepsOneCommittedSnapshot(t *testing.T) {
	for _, route := range []string{"run", "stack run", "stack runs"} {
		for _, boundary := range []string{"run", "history"} {
			t.Run(route+" after "+boundary, func(t *testing.T) {
				w := newWorld(t)
				run := mustCreate(t, w, kernel.RunTypeTracked, "snapshot")
				access := w.access()
				store := &interleavedRead{Store: w.store, boundary: boundary, commit: func() {
					receipt := policyReceipt(w, kernel.NewULID())
					if err := w.in(t, func(ctx context.Context) error {
						_, err := w.lc.Transition(ctx, readyWithReceipt(w, run, receipt))
						return err
					}); err != nil {
						t.Fatal(err)
					}
				}}
				reader := read.New(read.Deps{
					DB: w.db, Store: store, Stacks: w.stacks, Spaces: access, Authz: access,
					Evidence: w.store, Policies: deletedReadPolicies{}, Reviews: w.store,
					Approvals: w.store, Plans: w.store,
				})
				var view read.View
				var err error
				switch route {
				case "run":
					view, err = reader.Run(t.Context(), w.alice(), run.ID.String())
				case "stack run":
					view, err = reader.StackRun(t.Context(), w.alice(), "network", run.ID.String())
				case "stack runs":
					var page read.Page
					page, err = reader.StackRuns(t.Context(), w.alice(), "network", read.ListRuns, read.Query{Limit: 10})
					if err == nil && len(page.Views) == 1 {
						view = page.Views[0]
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if store.commit != nil {
					t.Fatal("projection did not cross the controlled writer commit")
				}
				if view.Run.State != kernel.RunStateQueued || view.Run.Version != 1 ||
					len(view.History) != 1 || view.History[0].State != kernel.RunStateQueued ||
					len(view.PolicyReceipts) != 0 || len(view.Flags) != 0 {
					t.Fatalf("mixed snapshot: state=%s/%d history=%v receipts=%d flags=%v",
						view.Run.State, view.Run.Version, view.History, len(view.PolicyReceipts), view.Flags)
				}
				// A fresh request must see the committed transition and its policy
				// effects; keeping a coherent snapshot must not cache the old view.
				view, err = reader.Run(t.Context(), w.alice(), run.ID.String())
				if err != nil || view.Run.State != kernel.RunStateReady || view.Run.Version != 2 ||
					len(view.History) != 2 || view.History[1].State != kernel.RunStateReady ||
					len(view.PolicyReceipts) != 1 || !slices.Equal(view.Flags, []string{"reviewed"}) {
					t.Fatalf("fresh snapshot: state=%s/%d history=%v receipts=%d flags=%v error=%v",
						view.Run.State, view.Run.Version, view.History, len(view.PolicyReceipts), view.Flags, err)
				}
			})
		}
	}
}
