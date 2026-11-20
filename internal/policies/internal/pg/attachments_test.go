package pg_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccAttachmentConcurrentPromotion(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Concurrent")
	stack := kernel.NewULID()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	g, ctx := errgroup.WithContext(ctx)
	start := make(chan struct{})
	var explicit core.Attachment
	for i := range 16 {
		g.Go(func() error {
			select {
			case <-start:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			return tx.Do(ctx, f.db, tx.Options{}, func(ctx context.Context) error {
				if err := f.store.LockAutoattach(ctx, p.Account); err != nil {
					return err
				}
				a := core.Attachment{Account: p.Account, ID: kernel.NewULID(), Policy: p.ID, Stack: stack, CreatedAt: f.now}
				if i == 0 {
					var err error
					explicit, err = f.store.Attach(ctx, a)
					return err
				}
				_, err := f.store.AutoAttach(ctx, a)
				return err
			})
		})
	}
	close(start)
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	all, err := f.store.Attachments(t.Context(), p.Account, []kernel.ULID{stack}, true)
	if err != nil || len(all[stack]) != 1 || all[stack][0].Autoattached || all[stack][0].ID != explicit.ID {
		t.Fatalf("promotion lost or duplicated: %+v %v", all, err)
	}
	wrong, err := f.store.AccountAttachments(t.Context(), kernel.NewAccountID())
	if err != nil || len(wrong) != 0 {
		t.Fatalf("cross-account relations: %+v %v", wrong, err)
	}
	p.Labels = []string{"autoattach:prod"}
	if err := f.store.Update(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	matched, err := f.store.Labelled(t.Context(), p.Account, []kernel.ULID{p.Space}, []string{"autoattach:prod"})
	if err != nil || len(matched) != 1 || matched[0].ID != p.ID {
		t.Fatalf("labelled: %+v %v", matched, err)
	}
	matched, err = f.store.AutomaticPolicies(t.Context(), p.Account)
	if err != nil || len(matched) != 1 || matched[0].ID != p.ID {
		t.Fatalf("automatic policies: %+v %v", matched, err)
	}
	for _, acct := range []kernel.AccountID{p.Account, kernel.NewAccountID()} {
		got, err := f.store.AccountAttachments(t.Context(), acct)
		want := 0
		if acct == p.Account {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Fatalf("account inventory: %+v %v", got, err)
		}
	}
	if err := f.store.DeleteStack(t.Context(), p.Account, stack); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "policy_stack_attachments") != 0 {
		t.Fatal("stack deletion left an owned relation")
	}
}
