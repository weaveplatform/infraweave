package pg_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

type retryBegin struct {
	tx.Beginner
	starts  int
	between func() error
}

func (s *retryBegin) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	s.starts++
	if s.starts == 2 {
		if err := s.between(); err != nil {
			return nil, err
		}
	}
	return s.Beginner.BeginTx(ctx, opts)
}

// PostgreSQL actually rejects the first update with serialization_failure. The
// begin hook commits a second writer between attempts, before either retry lock.
func TestAccOmittedEnginePreservesInterveningCommitAfterRetry(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Original")
	_, err := f.db.Exec(t.Context(), `CREATE SEQUENCE policy_retry;
CREATE FUNCTION retry_policy_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF nextval('policy_retry')=1 THEN RAISE EXCEPTION 'retry policy write' USING ERRCODE='40001'; END IF; RETURN NEW; END $$;
CREATE TRIGGER retry_policy_once BEFORE UPDATE ON policies FOR EACH ROW EXECUTE FUNCTION retry_policy_once()`)
	if err != nil {
		t.Fatal(err)
	}
	db := &retryBegin{Beginner: f.db, between: func() error {
		_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.ExpectVersions(1), core.Input{Name: "Intervening", Body: p.Body, Engine: rego.V0})
		return err
	}}
	svc := core.New(core.Deps{Store: f.store, DB: db, Spaces: f.spaces, Authz: f.authz, Runtime: f.runtime, Events: f.events})
	out, err := svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, core.Input{Name: "Retried", Body: p.Body})
	if err != nil || out.Engine != rego.V0 || out.Version != 3 || db.starts != 2 {
		t.Fatalf("omitted engine overwrote intervening commit: %+v starts=%d %v", out, db.starts, err)
	}
	if f.count(t, "events") != 3 {
		t.Fatal("aborted attempt appended an event or committed writer lost its event")
	}
}
