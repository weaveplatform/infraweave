package integrity_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/platform/sql/integrity"
	"github.com/weaveplatform/infraweave/internal/platform/sql/schema"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

func TestMain(m *testing.M) { os.Exit(sqltest.Main(m, schema.Stream())) }

var (
	keys = []integrity.Key{{Entity: "owner", Table: "owners", Columns: []string{"a", "id"}}}
	held = integrity.Held{Entity: "owner", Table: "holders", Columns: []string{"a", "owner_id"}}
)

// TestAccCount counts each distinct held ID whose owner row is gone once,
// ignores a null one and one whose row is there, and matches on every
// key column, so an ID held under another account is gone too.
func TestAccCount(t *testing.T) {
	ctx := t.Context()
	db, err := pgxpool.New(ctx, sqltest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if _, err := db.Exec(ctx, `
		CREATE TABLE owners (a int, id int, PRIMARY KEY (a, id));
		CREATE TABLE holders (a int, owner_id int);
		INSERT INTO owners VALUES (1, 1);
		INSERT INTO holders VALUES (1, 1), (1, 2), (1, 2), (1, NULL), (2, 1);`); err != nil {
		t.Fatal(err)
	}
	got, err := integrity.Count(ctx, db, keys, []integrity.Held{held})
	if err != nil || len(got) != 1 || got[0].Held.Table != "holders" || got[0].Count != 2 {
		t.Errorf("Count = %+v, %v; want 2 of holders", got, err)
	}
	for name, h := range map[string]integrity.Held{
		"an undeclared entity":  {Entity: "nobody", Table: "holders", Columns: []string{"a"}},
		"columns that mismatch": {Entity: "owner", Table: "holders", Columns: []string{"a"}},
	} {
		if _, err := integrity.Count(ctx, db, keys, []integrity.Held{h}); !errors.Is(
			err, integrity.ErrUndeclared) {
			t.Errorf("%s = %v, want ErrUndeclared", name, err)
		}
	}
	db.Close()
	if _, err := integrity.Count(context.Background(), db, keys,
		[]integrity.Held{held}); err == nil {
		t.Error("a closed pool counted")
	}
}

func TestUnitHeldString(t *testing.T) {
	if got := held.String(); got != "holders(a, owner_id)" {
		t.Errorf("String = %q", got)
	}
}
