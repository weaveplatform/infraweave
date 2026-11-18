package queuetest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// Capture each public method's actual statement, so its plan proof cannot drift
// into explaining a copy of a query the method no longer executes.
func TestAccKeyScopePlans(t *testing.T) {
	_, pool := open(t)
	account := kernel.NewULID()
	if _, err := pool.Exec(t.Context(), `INSERT INTO jobs
		(queue,kind,payload,account_id,unique_key,priority,run_at,state,max_attempts,finished_at)
		SELECT 'default','test.work','{}',CASE WHEN n=1 THEN $1::uuid ELSE gen_random_uuid() END,
		CASE WHEN n=1 THEN 'target:r1:parent' ELSE 'unrelated:'||n::text END,
		5,now(),'dead',1,now() FROM generate_series(1,12000) n`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `ANALYZE jobs`); err != nil {
		t.Fatal(err)
	}
	trace := &keyScopeTrace{}
	config := pool.Config().Copy()
	config.ConnConfig.Tracer = trace
	traced, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer traced.Close()
	q := queue.New(traced)
	if got, err := q.StatusByKeyPrefixes(t.Context(), account, []queue.Kind{work}, []string{"target:"}); err != nil || got["target:"].Dead != 1 {
		t.Fatal("missing keyed status", got, err)
	}
	assertAccountKeyPlan(t, pool, trace, "status")
	if err := tx.Do(t.Context(), traced, tx.Options{}, func(ctx context.Context) error {
		_, err := q.HoldByKeyPrefixTx(ctx, account, []queue.Kind{work}, "target:")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertAccountKeyPlan(t, pool, trace, "prefix lock")
	if err := tx.Do(t.Context(), traced, tx.Options{}, func(ctx context.Context) error {
		found, err := q.DeadByKeyTx(ctx, work, "target:r1:parent", account)
		if !found && err == nil {
			t.Error("missing dead job")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertAccountKeyPlan(t, pool, trace, "dead key")
}

type keyScopeTrace struct {
	sql  string
	args []any
}

func (tr *keyScopeTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "jobs") {
		tr.sql, tr.args = data.SQL, append([]any(nil), data.Args...)
	}
	return ctx
}

func (*keyScopeTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type keyPlan struct {
	Index string    `json:"Index Name"` //nolint:tagliatelle // PostgreSQL EXPLAIN fields
	Cond  string    `json:"Index Cond"` //nolint:tagliatelle // as above
	Plans []keyPlan `json:"Plans"`      //nolint:tagliatelle // as above
}

func accountKeyIndex(plan keyPlan) bool {
	if plan.Index == "jobs_key_scope" && strings.Contains(plan.Cond, "account_id") {
		return true
	}
	for _, child := range plan.Plans {
		if accountKeyIndex(child) {
			return true
		}
	}
	return false
}

func assertAccountKeyPlan(t *testing.T, pool *pgxpool.Pool, trace *keyScopeTrace, name string) {
	t.Helper()
	if trace.sql == "" {
		t.Fatal("no actual queue query was captured")
	}
	var encoded []byte
	if err := pool.QueryRow(t.Context(), `EXPLAIN (FORMAT JSON) `+trace.sql, trace.args...).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var nodes []struct {
		Plan keyPlan `json:"Plan"` //nolint:tagliatelle // PostgreSQL EXPLAIN field
	}
	if err := json.Unmarshal(encoded, &nodes); err != nil || len(nodes) != 1 || !accountKeyIndex(nodes[0].Plan) {
		t.Fatalf("%s query did not constrain its index by account: %s error=%v", name, encoded, err)
	}
	t.Logf("%s account-bound plan: %s", name, encoded)
}
