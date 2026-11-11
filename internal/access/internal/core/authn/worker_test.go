package authn_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// runs is accessapi.Runs over a map; a run it does not hold is
// ErrNoSubject.
type runs struct {
	runs map[kernel.ULID]accessapi.RunAccess
	err  error
}

func (r *runs) RunAccess(
	_ context.Context,
	_ kernel.AccountID,
	run kernel.ULID,
) (accessapi.RunAccess, error) {
	if r.err != nil {
		return accessapi.RunAccess{}, r.err
	}
	ra, ok := r.runs[run]
	if !ok {
		return accessapi.RunAccess{}, accessapi.ErrNoSubject
	}
	return ra, nil
}

func TestUnitWorkerTokens(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	worker, run := kernel.NewULID(), kernel.NewULID()
	wt, err := w.s.WorkerToken(ctx, worker)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := w.s.RunToken(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if !wt.Expires.Equal(t0.Add(time.Hour)) || !rt.Expires.Equal(t0.Add(24*time.Hour)) {
		t.Errorf("expiries = %s, %s", wt.Expires, rt.Expires)
	}
	if got, err := w.s.VerifyWorker(ctx, wt.Raw); err != nil || got != worker {
		t.Errorf("VerifyWorker = %s, %v", got, err)
	}
	if got, err := w.s.VerifyRun(ctx, rt.Raw); err != nil || got != run {
		t.Errorf("VerifyRun = %s, %v", got, err)
	}
	api, _ := w.exchange(t)
	// Each audience takes only its own kind: a token of the right audience
	// but another kind, or whose sub is no ULID, is refused too.
	forged := func(kind token.Kind, aud, sub string) string {
		id := kernel.NewULID()
		w.f.subjects[id] = sub
		return fmt.Sprintf("%s %s %s", kind, id, aud)
	}
	for name, tc := range map[string]struct {
		raw    string
		verify func(context.Context, string) (kernel.ULID, error)
	}{
		"run token as worker's": {rt.Raw, w.s.VerifyWorker},
		"worker token as run's": {wt.Raw, w.s.VerifyRun},
		"API token as worker's": {api, w.s.VerifyWorker},
		"API token as run's":    {api, w.s.VerifyRun},
		"another kind":          {forged(token.KindAPIKey, accessapi.WorkerAudience, worker.String()), w.s.VerifyWorker},
		"sub not a ULID":        {forged(token.KindRunner, accessapi.RunnerAudience, "stack/run"), w.s.VerifyRun},
		"garbage":               {"garbage", w.s.VerifyWorker},
	} {
		if _, err := tc.verify(ctx, tc.raw); !errors.Is(err, accessapi.ErrInvalidToken) {
			t.Errorf("%s = %v", name, err)
		}
	}
	// Neither is a credential on the API.
	for name, raw := range map[string]string{"worker": wt.Raw, "run": rt.Raw} {
		if _, err := authenticate(t, w.s, raw); !errors.Is(err, authn.ErrUnauthenticated) {
			t.Errorf("%s token on the API = %v", name, err)
		}
	}
	w.f.fail = "Verify"
	if _, err := w.s.VerifyWorker(ctx, wt.Raw); !errors.Is(err, errDown) {
		t.Errorf("Verify down = %v", err)
	}
	w.f.fail = "Issue"
	if _, err := w.s.WorkerToken(ctx, worker); !errors.Is(err, errDown) {
		t.Errorf("Issue down = %v", err)
	}
}

func TestUnitInRunToken(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	run, stack, space := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	req := accessapi.InRunRequest{Account: w.acct, Run: run, Slug: "infra"}
	tok, err := w.s.InRunToken(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !tok.Expires.Equal(t0.Add(3 * time.Hour)) {
		t.Errorf("expiry = %s", tok.Expires)
	}
	// Until runs is bound, no in-run token authenticates.
	if _, err := authenticate(t, w.s, tok.Raw); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("unbound = %v", err)
	}
	r := &runs{runs: map[kernel.ULID]accessapi.RunAccess{
		run: {Stack: stack, Space: space, Live: true},
	}}
	w.s.BindRuns(r)
	p, err := authenticate(t, w.s, tok.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != kernel.PrincipalRun || p.ActorID != run || p.AccountID != w.acct ||
		p.OrganizationID != w.f.org || p.StackID != stack || p.StackSpaceID != space ||
		p.Login != "infra/"+run.String() {
		t.Errorf("principal = %+v", p)
	}
	// A run acts only in its account.
	if _, _, err := w.s.Authenticate(
		ctx, tok.Raw, authn.ReachOrganization,
	); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("organization route = %v", err)
	}
	// The run's state is read at each request.
	r.runs[run] = accessapi.RunAccess{Stack: stack, Space: space}
	if _, err := authenticate(t, w.s, tok.Raw); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("run not live = %v", err)
	}
	delete(r.runs, run)
	if _, err := authenticate(t, w.s, tok.Raw); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("no such run = %v", err)
	}
	r.err = errDown
	if _, err := authenticate(t, w.s, tok.Raw); !errors.Is(err, errDown) {
		t.Errorf("runs down = %v", err)
	}
	r.err = nil
	r.runs[run] = accessapi.RunAccess{Stack: stack, Space: space, Live: true}
	if _, err := w.s.Revoke(ctx, p, p.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := authenticate(t, w.s, tok.Raw); !errors.Is(err, authn.ErrUnauthenticated) {
		t.Errorf("revoked = %v", err)
	}

	for name, bad := range map[string]accessapi.InRunRequest{
		"no slug":    {Account: w.acct, Run: run},
		"no run":     {Account: w.acct, Slug: "infra"},
		"no account": {Run: run, Slug: "infra"},
	} {
		if _, err := w.s.InRunToken(ctx, bad); !errors.Is(err, token.ErrRequest) {
			t.Errorf("%s = %v", name, err)
		}
	}
	for _, op := range []string{"Account", "Issue", "CreateSession"} {
		w.f.fail = op
		if _, err := w.s.InRunToken(ctx, req); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", op, err)
		}
	}
}
