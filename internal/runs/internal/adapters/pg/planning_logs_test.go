package pg_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
)

type planningObjects struct {
	bodies map[string]string
	opens  []string
}

func (o *planningObjects) Open(_ context.Context, _ objectstore.Name, key string) (io.ReadCloser, error) {
	o.opens = append(o.opens, key)
	body, ok := o.bodies[key]
	if !ok {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func TestAccPlanningLogProjection(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, id, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	// Incompatible private fields and a nil sealer make a full lifecycle read
	// fail; the planning port needs only the owned transition and chunk index.
	if _, err := db.Exec(t.Context(), `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by,
  runtime_config,resolved_config,env_overrides_sealed,artifact_key_sealed)
  VALUES($1,$2,$3,'TRACKED','FINISHED','{"hash":"sha","message":{"private":true}}','private user',
   '["private runtime"]','"private configuration"','unreadable','unreadable')`, account, id, stack); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int64{3, 7} {
		if _, err := db.Exec(t.Context(), `INSERT INTO run_state_transitions(account_id,run_id,sequence,state,terminal,log_chunks)
   VALUES($1,$2,$3,'PLANNING',false,2)`, account, id, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO run_state_transitions(account_id,run_id,sequence,state,terminal)
  VALUES($1,$2,4,'UNCONFIRMED',false),($1,$2,8,'FINISHED',true)`, account, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO run_log_chunks(account_id,run_id,state_version,seq,state,object_key,bytes,first_at,last_at)
  VALUES($1,$2,3,0,'PLANNING','first',12,now(),now()),($1,$2,3,1,'PLANNING','tail',4,now(),now()),
   ($1,$2,7,0,'PLANNING','later',9,now(),now())`, account, id); err != nil {
		t.Fatal(err)
	}
	objects := &planningObjects{bodies: map[string]string{"first": "masked=***\n", "tail": "tail\n", "later": "later planning\n"}}
	reader := logs.NewPlanningReader(store, objects)
	got, err := reader.PlanningLog(t.Context(), account, id, 3, 100)
	if err != nil || got != (runsapi.PlanningLog{Text: "masked=***\ntail\n"}) {
		t.Fatal("exact old planning state", got, err)
	}
	got, err = reader.PlanningLog(t.Context(), account, id, 7, 100)
	if err != nil || got != (runsapi.PlanningLog{Text: "later planning\n" + logs.Missing + "\n", Unavailable: true}) {
		t.Fatal("current planning and unindexed extent", got, err)
	}
	before := len(objects.opens)
	for _, scope := range []struct {
		account kernel.AccountID
		run     kernel.ULID
		version int64
	}{
		{kernel.NewAccountID(), id, 3}, {account, kernel.NewULID(), 3}, {account, id, 4}, {account, id, 6},
	} {
		if _, err := reader.PlanningLog(t.Context(), scope.account, scope.run, scope.version, 100); !errors.Is(err, runsapi.ErrPlanningLogNotFound) {
			t.Fatal("projection escaped exact account/run/PLANNING version", err)
		}
	}
	if len(objects.opens) != before {
		t.Fatal("refused scope opened an object")
	}
	if _, err := db.Exec(t.Context(), `UPDATE runs SET logs_deleted_at=now() WHERE account_id=$1 AND id=$2`, account, id); err != nil {
		t.Fatal(err)
	}
	if got, err := reader.PlanningLog(t.Context(), account, id, 3, 100); err != nil || got != (runsapi.PlanningLog{Unavailable: true}) || len(objects.opens) != before {
		t.Fatal("deleted log read objects", got, err)
	}
	db.Close()
	_, cause := db.Exec(t.Context(), "SELECT 1")
	if _, err := reader.PlanningLog(t.Context(), account, id, 3, 100); cause == nil || !errors.Is(err, cause) {
		t.Fatal("dependency failure became not found", err)
	}
}

func TestAccPlanningLogCallerTransaction(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, id, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	objects := &planningObjects{bodies: map[string]string{"uncommitted": "own transaction\n"}}
	reader := logs.NewPlanningReader(store, objects)
	rollback := errors.New("outer rollback")
	err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		for n, query := range []string{
			`INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)VALUES($1,$2,$3,'PROPOSED','FINISHED','{"hash":"sha"}','manual')`,
			`INSERT INTO run_state_transitions(account_id,run_id,sequence,state,terminal,log_chunks)SELECT $1,$2,3,'PLANNING',false,NULL FROM runs WHERE stack_id=$3 AND id=$2`,
			`INSERT INTO run_log_chunks(account_id,run_id,state_version,seq,state,object_key,bytes,first_at,last_at)SELECT $1,$2,3,0,'PLANNING','uncommitted',16,now(),now() FROM runs WHERE stack_id=$3 AND id=$2`,
		} {
			if _, err := tx.Q(ctx, db).Exec(ctx, query, account, id, stack); err != nil {
				return err
			}
			if n == 1 {
				if got, err := reader.PlanningLog(ctx, account, id, 3, 100); err != nil || got != (runsapi.PlanningLog{}) {
					t.Fatal("NULL empty extent is not an available empty log", got, err)
				}
			}
		}
		got, err := reader.PlanningLog(ctx, account, id, 3, 100)
		if err != nil || got != (runsapi.PlanningLog{Text: "own transaction\n"}) {
			t.Fatal("caller cannot read own transition/chunk", got, err)
		}
		//nolint:contextcheck // the isolation read deliberately excludes the caller transaction
		if _, err := reader.PlanningLog(t.Context(), account, id, 3, 100); !errors.Is(err, runsapi.ErrPlanningLogNotFound) {
			t.Fatal("uncommitted planning log escaped", err)
		}
		if _, err := tx.Q(ctx, db).Exec(ctx, `UPDATE runs SET logs_deleted_at=now() WHERE account_id=$1 AND id=$2`, account, id); err != nil {
			return err
		}
		if got, err := reader.PlanningLog(ctx, account, id, 3, 100); err != nil || got != (runsapi.PlanningLog{Unavailable: true}) {
			t.Fatal("caller deletion was ignored", got, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := reader.PlanningLog(t.Context(), account, id, 3, 100); !errors.Is(err, runsapi.ErrPlanningLogNotFound) {
		t.Fatal("rolled-back planning log remains", err)
	}
}
