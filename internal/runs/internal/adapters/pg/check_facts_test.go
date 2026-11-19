package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

func TestAccCheckFacts(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account := kernel.NewAccountID()
	stacks := []kernel.ULID{kernel.NewULID(), kernel.NewULID()}
	sha := strings.Repeat("a", 40)
	at := time.Unix(1800000000, 0).UTC()
	want := make([]runsapi.CheckRun, 0, 63)
	deltas := []struct {
		json *string
		want *kernel.PlanDelta
	}{
		{nil, nil},
		{new("null"), nil},
		{new("{}"), &kernel.PlanDelta{}},
		{new(`{"outputs":true}`), &kernel.PlanDelta{Outputs: true}},
		{
			new(`{"add":1,"change":2,"delete":3,"import":4,"move":5,"forget":6,"resources":7}`),
			&kernel.PlanDelta{Add: 1, Change: 2, Delete: 3, Import: 4, Move: 5, Forget: 6, Resources: 7},
		},
	}
	for n := range 63 {
		typ := kernel.RunTypes()[n%len(kernel.RunTypes())]
		fact := runsapi.CheckRun{
			ID: kernel.NewULID(), Stack: stacks[n%2], Type: typ,
			State: kernel.RunStates()[n%len(kernel.RunStates())], Sequence: int64(n + 1),
			CommitSHA: sha, Branch: "main", Tag: "release", Local: n%3 == 2, DriftDetection: n%2 == 0,
			Delta: deltas[n%len(deltas)].want, CreatedAt: at.Add(time.Duration(n/3) * time.Minute), UpdatedAt: at.Add(time.Duration(n) * time.Hour),
		}
		var command, local *string
		if typ == kernel.RunTypeTask {
			command = new("private command")
		}
		if n%3 == 1 {
			local = new("")
		} else if fact.Local {
			local = new("private workspace key")
		}
		// The nil sealer and incompatible private JSON shapes make accidentally
		// loading the full run fail, rather than merely hiding it in the result.
		if _, err := db.Exec(t.Context(), `INSERT INTO runs
   (account_id,id,stack_id,type,state,state_version,commit,triggered_by,command,local_workspace,
    drift_detection,delta,created_at,updated_at,runtime_config,resolved_config,env_overrides_sealed,artifact_key_sealed)
   VALUES($1,$2,$3,$4,$5,$6,jsonb_build_object('hash',$7::text,'branch','main','tag','release','message',jsonb_build_object('private',true)),
    $8,$9,$10,$11,$12::jsonb,$13,$14,'["private runtime"]','"private configuration"','unreadable','unreadable')`,
			account, fact.ID, fact.Stack, typ, fact.State, fact.Sequence, sha, []string{"manual", "vcs/commit", "schedule"}[n%3], command, local,
			fact.DriftDetection, deltas[n%len(deltas)].json, fact.CreatedAt, fact.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		want = append(want, fact)
	}
	for _, scope := range []struct {
		account kernel.AccountID
		stack   kernel.ULID
		sha     string
	}{
		{kernel.NewAccountID(), stacks[0], sha}, {account, kernel.NewULID(), sha}, {account, stacks[0], "other"},
	} {
		insertCheckFact(t, db, scope.account, kernel.NewULID(), scope.stack, scope.sha)
	}
	slices.SortFunc(want, func(a, b runsapi.CheckRun) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	got, err := store.CheckRunsOnCommit(t.Context(), account, append(stacks, stacks[0]), sha)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("all current facts: got=%+v want=%+v error=%v", got, want, err)
	}
	for _, fact := range want {
		got, err := store.CheckRun(t.Context(), account, fact.ID)
		if err != nil || !reflect.DeepEqual(got, fact) {
			t.Fatalf("current run: got=%+v want=%+v error=%v", got, fact, err)
		}
	}
	for _, ids := range [][]kernel.ULID{nil, {}, {kernel.NewULID()}} {
		got, err := store.CheckRunsOnCommit(t.Context(), account, ids, sha)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("empty membership: %+v %v", got, err)
		}
	}
	if got, err := store.CheckRunsOnCommit(t.Context(), account, stacks, "missing"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("missing commit: %+v %v", got, err)
	}
	for _, scope := range []struct {
		account kernel.AccountID
		id      kernel.ULID
	}{
		{account, kernel.NewULID()}, {kernel.NewAccountID(), want[0].ID}, {kernel.AccountID{}, want[0].ID}, {account, kernel.ULID{}},
	} {
		if _, err := store.CheckRun(t.Context(), scope.account, scope.id); !errors.Is(err, runsapi.ErrCheckRunNotFound) {
			t.Fatalf("absent or foreign run: %v", err)
		}
	}
}

func insertCheckFact(t *testing.T, db tx.Querier, account kernel.AccountID, id, stack kernel.ULID, sha string) {
	t.Helper()
	if _, err := db.Exec(t.Context(), `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)
  VALUES($1,$2,$3,'PROPOSED','FINISHED',jsonb_build_object('hash',$4::text),'manual')`, account, id, stack, sha); err != nil {
		t.Fatal(err)
	}
}

func TestAccCheckFactsTransaction(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, id, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	rollback := errors.New("caller rollback")
	err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, db).Exec(ctx, `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by,delta)
   VALUES($1,$2,$3,'TRACKED','UNCONFIRMED','{"hash":"current"}','manual','{"outputs":true}')`, account, id, stack); err != nil {
			return err
		}
		one, err := store.CheckRun(ctx, account, id)
		if err != nil || one.ID != id || one.Delta == nil || !one.Delta.Outputs {
			t.Fatalf("own uncommitted facts: %+v %v", one, err)
		}
		many, err := store.CheckRunsOnCommit(ctx, account, []kernel.ULID{stack}, "current")
		if err != nil || len(many) != 1 || !reflect.DeepEqual(many[0], one) {
			t.Fatalf("own uncommitted membership: %+v %v", many, err)
		}
		//nolint:contextcheck // this isolation read deliberately excludes the caller transaction
		if _, err := store.CheckRun(t.Context(), account, id); !errors.Is(err, runsapi.ErrCheckRunNotFound) {
			t.Fatalf("uncommitted run escaped: %v", err)
		}
		//nolint:contextcheck // as above
		if got, err := store.CheckRunsOnCommit(t.Context(), account, []kernel.ULID{stack}, "current"); err != nil || len(got) != 0 {
			t.Fatalf("uncommitted membership escaped: %+v %v", got, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := store.CheckRun(t.Context(), account, id); !errors.Is(err, runsapi.ErrCheckRunNotFound) {
		t.Fatal("rollback left run", err)
	}
	if got, err := store.CheckRunsOnCommit(t.Context(), account, []kernel.ULID{stack}, "current"); err != nil || len(got) != 0 {
		t.Fatal("rollback left membership", got, err)
	}
	insertCheckFact(t, db, account, id, stack, "current")
	err = tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, db).Exec(ctx, `UPDATE runs SET state='FAILED',state_version=7,delta='{}' WHERE account_id=$1 AND id=$2`, account, id); err != nil {
			return err
		}
		got, err := store.CheckRun(ctx, account, id)
		if err != nil || got.State != kernel.RunStateFailed || got.Sequence != 7 || got.Delta == nil {
			t.Fatalf("updated current facts: %+v %v", got, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if got, err := store.CheckRun(t.Context(), account, id); err != nil || got.Sequence != 1 || got.Delta != nil {
		t.Fatalf("rollback changed current facts: %+v %v", got, err)
	}
}

func TestAccCheckFactsErrors(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, id, stack := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	insertCheckFact(t, db, account, id, stack, "current")
	for _, delta := range []string{`[]`, `"invalid"`, `{"add":"invalid"}`, `{"outputs":7}`} {
		if _, err := db.Exec(t.Context(), `UPDATE runs SET delta=$3::jsonb WHERE account_id=$1 AND id=$2`, account, id, delta); err != nil {
			t.Fatal(err)
		}
		_, one := store.CheckRun(t.Context(), account, id)
		_, many := store.CheckRunsOnCommit(t.Context(), account, []kernel.ULID{stack}, "current")
		for _, err := range []error{one, many} {
			var malformed *json.UnmarshalTypeError
			if !errors.As(err, &malformed) || errors.Is(err, runsapi.ErrCheckRunNotFound) {
				t.Fatalf("malformed %s silently lost: %v", delta, err)
			}
		}
	}
	db.Close()
	_, cause := db.Exec(t.Context(), "SELECT 1")
	_, one := store.CheckRun(t.Context(), account, id)
	_, many := store.CheckRunsOnCommit(t.Context(), account, []kernel.ULID{stack}, "current")
	for _, err := range []error{one, many} {
		if cause == nil || !errors.Is(err, cause) {
			t.Fatalf("database error lost: %v", err)
		}
	}
}

type checkFactQuery struct {
	sql  string
	args []any
}

func (q *checkFactQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT ") {
		q.sql = data.SQL
		q.args = slices.Clone(data.Args)
	}
	return ctx
}
func (*checkFactQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAccCheckFactsQueryPlan(t *testing.T) {
	db := pool(t)
	account, stack, other := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	if _, err := db.Exec(t.Context(), `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)
  SELECT $1,gen_random_uuid(),CASE WHEN n%2=0 THEN $2::uuid ELSE $3::uuid END,
  CASE WHEN n%3=0 THEN 'TRACKED' ELSE 'PROPOSED' END,'FINISHED',
  jsonb_build_object('hash',lpad(to_hex(n),40,'0')),'manual' FROM generate_series(1,12000)n`, account, stack, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `ANALYZE runs`); err != nil {
		t.Fatal(err)
	}
	capture := new(checkFactQuery)
	cfg := db.Config()
	cfg.ConnConfig.Tracer = capture
	traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	got, err := pg.New(traced, nil).CheckRunsOnCommit(t.Context(), account, []kernel.ULID{stack, other}, strings.Repeat("0", 39)+"1")
	if err != nil || len(got) != 1 {
		t.Fatalf("sparse current lookup: %+v %v", got, err)
	}
	if capture.sql == "" {
		t.Fatal("public query was not captured")
	}
	var plan []byte
	if err := db.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+capture.sql, capture.args...).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	var nodes []struct {
		Plan queryPlan `json:"Plan"` //nolint:tagliatelle // PostgreSQL's field name
	}
	if err := json.Unmarshal(plan, &nodes); err != nil || len(nodes) != 1 || !checkFactIndex(nodes[0].Plan) {
		t.Fatalf("actual sparse query lacks scoped commit index: %s error=%v", plan, err)
	}
	t.Logf("actual check-facts query plan: %s", plan)
}

func checkFactIndex(plan queryPlan) bool {
	return plan.Index == "runs_commit" && strings.Contains(plan.Cond, "hash") && strings.Contains(plan.Cond, "account_id") && strings.Contains(plan.Cond, "stack_id") ||
		slices.ContainsFunc(plan.Plans, checkFactIndex)
}
