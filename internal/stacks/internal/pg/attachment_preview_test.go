package pg_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// The oldest created target becomes the newest updated target. Filtering and
// sampling happen before a hidden space or another account can affect counts.
func TestAccAttachmentPreviewCountsAndUpdatedOrder(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	acct, other := kernel.NewAccountID(), kernel.NewAccountID()
	space, hidden := kernel.NewULID(), kernel.NewULID()
	var oldest kernel.ULID
	for i := range 105 {
		id, owner := kernel.NewULID(), space
		if i == 0 {
			oldest = id
		}
		if i >= 103 {
			owner = hidden
		}
		if _, err := db.Exec(t.Context(), `INSERT INTO stacks(account_id,id,slug,space_id,name,repository,branch,provider,vendor,created_at,updated_at)
 VALUES($1,$2,$3,$4,$3,'infra','main','GIT','terraform',$5,$5)`, acct, id, fmt.Sprintf("target-%03d", i), owner, time.Unix(1600000000+int64(i), 0)); err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			if _, err := db.Exec(t.Context(), `INSERT INTO stack_labels(account_id,stack_id,label) VALUES($1,$2,'prod'),($1,$2,'dev')`, acct, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO stacks(account_id,id,slug,space_id,name,repository,branch,provider,vendor)
 VALUES($1,$2,'foreign',$3,'Foreign','infra','main','GIT','terraform')`, other, kernel.NewULID(), space); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunState(t.Context(), acct, oldest, kernel.StackState("NONE"), false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Preview(t.Context(), acct, []kernel.ULID{space}, nil, true)
	if err != nil || got.Count != 103 || len(got.Sample) != 100 || got.Sample[0].ID != oldest {
		t.Fatalf("updated sample/count: %+v %v", got, err)
	}
	seen := map[kernel.ULID]bool{}
	for _, target := range got.Sample {
		if target.Account != acct || target.Space != space || target.Slug == "foreign" || seen[target.ID] {
			t.Fatalf("hidden or duplicated target: %+v", target)
		}
		seen[target.ID] = true
	}
	got, err = s.Preview(t.Context(), acct, []kernel.ULID{space}, []string{"prod", "dev"}, false)
	if err != nil || got.Count != 5 || len(got.Sample) != 5 {
		t.Fatalf("matching two labels inflated count: %+v %v", got, err)
	}
	for _, target := range got.Sample {
		if !slices.Equal(target.Labels, []string{"dev", "prod"}) {
			t.Fatalf("current labels: %+v", target)
		}
	}
	for _, input := range []struct {
		acct   kernel.AccountID
		spaces []kernel.ULID
		labels []string
	}{
		{acct, nil, []string{"prod"}},
		{kernel.NewAccountID(), []kernel.ULID{space}, []string{"prod"}},
		{acct, []kernel.ULID{space}, []string{"absent"}},
	} {
		got, err := s.Preview(t.Context(), input.acct, input.spaces, input.labels, false)
		if err != nil || got.Count != 0 || len(got.Sample) != 0 || got.Sample == nil {
			t.Fatalf("empty/isolation preview: %+v %v", got, err)
		}
	}
	closed := pool(t)
	closed.Close()
	if _, err := pg.New(closed).Preview(t.Context(), acct, []kernel.ULID{space}, nil, true); err == nil {
		t.Fatal("unavailable store answered a successful empty preview")
	}
}

var _ stacksapi.AttachmentTargets = (*pg.Store)(nil)

// A transaction can start earlier but perform its write later. Preview order
// follows the actual update, rather than PostgreSQL's transaction-start now().
func TestAccAttachmentPreviewOrdersByWriteTime(t *testing.T) {
	db := pool(t)
	s := pg.New(db)
	acct := kernel.NewAccountID()
	space, first, last := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	for i, id := range []kernel.ULID{first, last} {
		if _, err := db.Exec(
			t.Context(),
			`INSERT INTO stacks(account_id,id,slug,space_id,name,repository,branch,provider,vendor) VALUES($1,$2,$3,$4,$3,'infra','main','GIT','terraform')`,
			acct,
			id,
			fmt.Sprintf("target-%d", i),
			space,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		var started time.Time
		if err := tx.Q(ctx, db).QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&started); err != nil {
			return err
		}
		// This separate committed write is newer than the enclosing transaction.
		if err := s.SetRunState(t.Context(), acct, first, kernel.StackState("NONE"), false); err != nil {
			return err
		}
		return s.SetRunState(ctx, acct, last, kernel.StackState("NONE"), false)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Preview(t.Context(), acct, []kernel.ULID{space}, nil, true)
	if err != nil || len(got.Sample) != 2 || got.Sample[0].ID != last {
		t.Fatalf("later write was backdated by an earlier transaction: %+v %v", got, err)
	}
}
