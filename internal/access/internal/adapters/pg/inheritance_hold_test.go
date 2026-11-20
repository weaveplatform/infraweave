package pg_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/test/deps"
)

// An inheritance-only update must wait for holds anywhere in the changed
// subtree, then validate the rows that the held transaction actually committed.
func TestAccInheritanceWaitsForDescendantHold(t *testing.T) {
	x := seed(t, pool(t), "inherit-fence")
	svc := treeOf(x.s)
	child, err := svc.Create(t.Context(), x.acct, "inherit-fence", space.Space{Name: "Child", InheritEntities: true})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := svc.Create(t.Context(), x.acct, child.Slug, space.Space{Name: "Grandchild", InheritEntities: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(t.Context(), "CREATE TABLE held_attachment_commit(marker boolean NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	g, ctx := errgroup.WithContext(ctx)
	held := make(chan int32, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer func() { cancel(); releaseOnce.Do(func() { close(release) }); _ = g.Wait() }()
	g.Go(func() error {
		return tx.Do(ctx, x.db, tx.Options{}, func(ctx context.Context) error {
			if err := x.s.HoldSpace(ctx, x.acct, grandchild.ID); err != nil {
				return err
			}
			var pid int32
			if err := tx.Q(ctx, x.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				return err
			}
			held <- pid
			select {
			case <-release:
			case <-ctx.Done():
				return context.Cause(ctx)
			}
			_, err := tx.Q(ctx, x.db).Exec(ctx, "INSERT INTO held_attachment_commit VALUES(true)")
			return err
		})
	})
	var blocker int32
	select {
	case blocker = <-held:
	case <-ctx.Done():
		t.Fatal(context.Cause(ctx))
	}
	finished := make(chan struct{})
	var observed int
	g.Go(func() error {
		defer close(finished)
		_, err := x.s.UpdateSpace(ctx, x.acct, child.Slug, "inherit-fence", false, func(ctx context.Context, cur, _ space.Space, _ *accessapi.SpaceMove) (space.Space, error) {
			if err := tx.Q(ctx, x.db).QueryRow(ctx, "SELECT count(*) FROM held_attachment_commit").Scan(&observed); err != nil {
				return space.Space{}, err
			}
			cur.InheritEntities = false
			return cur, nil
		})
		return err
	})
	if err := deps.Eventually(ctx, "inheritance update blocked by descendant hold", func(ctx context.Context) (bool, error) {
		select {
		case <-finished:
			return false, fmt.Errorf("%w: update bypassed the hold", deps.ErrPermanent)
		default:
		}
		var waiting bool
		err := x.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))", blocker).Scan(&waiting)
		return waiting, err
	}); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	if observed != 1 {
		t.Fatalf("guard missed the transaction committed under the hold: %d", observed)
	}
	current, err := x.s.Space(t.Context(), x.acct, child.Slug)
	if err != nil || current.InheritEntities {
		t.Fatalf("inheritance change: %+v %v", current, err)
	}
}
