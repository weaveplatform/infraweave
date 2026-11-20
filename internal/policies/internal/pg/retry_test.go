package pg_test

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

type retryRead struct {
	core.Store
	reads   int
	between func() error
}

func (s *retryRead) Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID, lock bool) (core.Policy, error) {
	if lock {
		s.reads++
		if s.reads == 2 {
			if err := s.between(); err != nil {
				return core.Policy{}, err
			}
		}
	}
	return s.Store.Get(ctx, acct, id, lock)
}

// PostgreSQL actually rejects the first update with serialization_failure. The
// read hook commits a second writer between attempts, before the retry's lock.
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
	store := &retryRead{Store: f.store, between: func() error {
		_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.ExpectVersions(1), core.Input{Name: "Intervening", Body: p.Body, Engine: rego.V0})
		return err
	}}
	svc := core.New(core.Deps{Store: store, DB: f.db, Spaces: f.spaces, Authz: f.authz, Runtime: f.runtime, Events: f.events})
	out, err := svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, core.Input{Name: "Retried", Body: p.Body})
	if err != nil || out.Engine != rego.V0 || out.Version != 3 || store.reads != 2 {
		t.Fatalf("omitted engine overwrote intervening commit: %+v reads=%d %v", out, store.reads, err)
	}
	if f.count(t, "events") != 3 {
		t.Fatal("aborted attempt appended an event or committed writer lost its event")
	}
}
