package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	"github.com/weaveplatform/infraweave/internal/runs/internal/adapters/pg"
)

func TestAccCommitRuns(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, stack := kernel.NewAccountID(), kernel.NewULID()
	at := time.Unix(1800000000, 0).UTC()
	current, historical := strings.Repeat("a", 40), strings.Repeat("b", 40)
	want := make([]runsapi.CommitRun, 0, 63)
	// More than fifty matching runs ensures the domain port leaves list limits
	// to its caller, after that caller establishes pull-request membership.
	for number := range 63 {
		id := kernel.NewULID()
		hash := current
		if number%2 == 0 {
			hash = historical
		}
		typ := kernel.RunTypes()[number%len(kernel.RunTypes())]
		state := kernel.RunStates()[number%len(kernel.RunStates())]
		created := at.Add(time.Duration(number/3) * time.Minute)
		var command *string
		if typ == kernel.RunTypeTask {
			command = new("private task command")
		}
		if _, err := db.Exec(t.Context(), `INSERT INTO runs
			(account_id,id,stack_id,type,state,commit,triggered_by,created_at,command,
			local_workspace,runtime_config,resolved_config,env_overrides_sealed,artifact_key_sealed)
			VALUES($1,$2,$3,$4,$5,jsonb_build_object('hash',$6::text,'message','private commit message'),
			$7,$8,$9,'private workspace','{"private":"runtime"}','{"private":"configuration"}',
			'unreadable','unreadable')`, account, id, stack, typ, state, hash, []string{"alice", "vcs/commit", "schedule"}[number%3], created, command); err != nil {
			t.Fatal(err)
		}
		want = append(want, runsapi.CommitRun{ID: id, CommitSHA: hash, CreatedAt: created})
	}
	for _, scope := range []struct {
		account kernel.AccountID
		stack   kernel.ULID
		hash    string
	}{
		{kernel.NewAccountID(), stack, current},
		{account, kernel.NewULID(), current},
		{account, stack, strings.Repeat("c", 40)},
	} {
		if _, err := db.Exec(t.Context(), `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)
			VALUES($1,$2,$3,'TRACKED','FINISHED',jsonb_build_object('hash',$4::text),'alice')`,
			scope.account, kernel.NewULID(), scope.stack, scope.hash); err != nil {
			t.Fatal(err)
		}
	}
	slices.SortFunc(want, func(a, b runsapi.CommitRun) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	got, err := store.OnCommits(t.Context(), account, stack, []string{current, historical, current})
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("all commit runs: got=%+v want=%+v error=%v", got, want, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "unreadable") {
		t.Fatal("commit projection exposed private run data", err)
	}
	for _, hashes := range [][]string{nil, {}, {"absent"}} {
		if got, err := store.OnCommits(t.Context(), account, stack, hashes); err != nil || len(got) != 0 {
			t.Fatalf("empty or missing commits: %v %v", got, err)
		}
	}
}

func TestAccCommitRunsTransaction(t *testing.T) {
	db := pool(t)
	store := pg.New(db, nil)
	account, stack, id := kernel.NewAccountID(), kernel.NewULID(), kernel.NewULID()
	hash := strings.Repeat("a", 40)
	rollback := errors.New("caller rolls back")
	err := tx.Do(t.Context(), db, tx.Options{}, func(ctx context.Context) error {
		if _, err := tx.Q(ctx, db).Exec(ctx, `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)
			VALUES($1,$2,$3,'PROPOSED','FINISHED',jsonb_build_object('hash',$4::text),'manual')`, account, id, stack, hash); err != nil {
			return err
		}
		got, err := store.OnCommits(ctx, account, stack, []string{hash})
		if err != nil || len(got) != 1 || got[0].ID != id {
			t.Fatalf("caller transaction cannot read its own run: %+v %v", got, err)
		}
		//nolint:contextcheck // the isolation read must not inherit the caller's transaction
		outside, err := store.OnCommits(t.Context(), account, stack, []string{hash})
		if err != nil || len(outside) != 0 {
			t.Fatalf("uncommitted run visible outside its transaction: %+v %v", outside, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if got, err := store.OnCommits(t.Context(), account, stack, []string{hash}); err != nil || len(got) != 0 {
		t.Fatalf("rolled-back run remains visible: %+v %v", got, err)
	}
	db.Close()
	_, cause := db.Exec(t.Context(), "SELECT 1")
	if _, err := store.OnCommits(t.Context(), account, stack, []string{hash}); cause == nil || !errors.Is(err, cause) {
		t.Fatalf("unavailable database was treated as an empty commit set: %v", err)
	}
}

func TestAccCommitRunsQueryPlan(t *testing.T) {
	db := pool(t)
	account, stack := kernel.NewAccountID(), kernel.NewULID()
	if _, err := db.Exec(t.Context(), `INSERT INTO runs(account_id,id,stack_id,type,state,commit,triggered_by)
		SELECT $1,gen_random_uuid(),$2,CASE WHEN n%2=0 THEN 'TRACKED' ELSE 'PROPOSED' END,
		'FINISHED',jsonb_build_object('hash',md5(n::text)),'manual'
		FROM generate_series(1,12000) n`, account, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `ANALYZE runs`); err != nil {
		t.Fatal(err)
	}
	var plan []byte
	if err := db.QueryRow(t.Context(), `EXPLAIN (FORMAT JSON) SELECT id,commit->>'hash',created_at FROM runs
		WHERE account_id=$1 AND stack_id=$2 AND commit->>'hash'=ANY($3::text[])
		ORDER BY created_at DESC,id DESC`, account, stack, []string{"c4ca4238a0b923820dcc509a6f75849b"}).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	var nodes []struct {
		Plan queryPlan `json:"Plan"` //nolint:tagliatelle // PostgreSQL's EXPLAIN field name
	}
	if err := json.Unmarshal(plan, &nodes); err != nil || len(nodes) != 1 || !commitIndex(nodes[0].Plan) {
		t.Fatalf("commit lookup does not use its hash index: %s error=%v", plan, err)
	}
	t.Logf("commit lookup plan: %s", plan)
}

type queryPlan struct {
	Index string      `json:"Index Name"` //nolint:tagliatelle // PostgreSQL's EXPLAIN field names
	Cond  string      `json:"Index Cond"` //nolint:tagliatelle // as above
	Plans []queryPlan `json:"Plans"`      //nolint:tagliatelle // as above
}

func commitIndex(plan queryPlan) bool {
	return plan.Index == "runs_commit" && strings.Contains(plan.Cond, "hash") || slices.ContainsFunc(plan.Plans, commitIndex)
}
