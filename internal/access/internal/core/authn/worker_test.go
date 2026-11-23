package authn_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

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

func (r *runs) RunAccess(_ context.Context, _ kernel.AccountID, run kernel.ULID) (accessapi.RunAccess, error) {
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
	wt, err := w.s.WorkerToken(ctx, worker, 7)
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
	if got, err := w.s.VerifyWorker(ctx, wt.Raw); err != nil || got.ID != worker ||
		!got.Expires.Equal(wt.Expires) || got.WorkerSerial != 7 {
		t.Errorf("VerifyWorker = %+v, %v", got, err)
	}
	if got, err := w.s.VerifyRun(ctx, rt.Raw); err != nil || got.ID != run ||
		!got.Expires.Equal(rt.Expires) {
		t.Errorf("VerifyRun = %+v, %v", got, err)
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
		verify func(context.Context, string) (accessapi.Bearer, error)
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
	p, c, err := w.s.AuthenticateWorker(ctx, wt.Raw)
	if err != nil || p.Kind != kernel.PrincipalWorker || p.ActorID != worker ||
		!p.AccountID.IsZero() || !c.Expiry.Equal(wt.Expires) || c.WorkerSerial != 7 {
		t.Errorf("AuthenticateWorker = %+v, %+v, %v", p, c, err)
	}
	for name, raw := range map[string]string{"run": rt.Raw, "API": api, "garbage": "garbage"} {
		if _, _, err := w.s.AuthenticateWorker(
			ctx,
			raw,
		); !errors.Is(
			err,
			authn.ErrUnauthenticated,
		) {
			t.Errorf("AuthenticateWorker of a %s token = %v", name, err)
		}
	}
	p, c, err = w.s.AuthenticateRunner(ctx, rt.Raw)
	if err != nil || p.Kind != kernel.PrincipalRunner || p.ActorID != run ||
		!p.AccountID.IsZero() || !c.Expiry.Equal(rt.Expires) {
		t.Errorf("AuthenticateRunner = %+v, %+v, %v", p, c, err)
	}
	for name, raw := range map[string]string{"worker": wt.Raw, "API": api} {
		if _, _, err := w.s.AuthenticateRunner(
			ctx,
			raw,
		); !errors.Is(
			err,
			authn.ErrUnauthenticated,
		) {
			t.Errorf("AuthenticateRunner of a %s token = %v", name, err)
		}
	}
	w.f.fail = "Verify"
	if _, err := w.s.VerifyWorker(ctx, wt.Raw); !errors.Is(err, errDown) {
		t.Errorf("Verify down = %v", err)
	}
	if _, _, err := w.s.AuthenticateWorker(ctx, wt.Raw); !errors.Is(err, errDown) {
		t.Errorf("AuthenticateWorker with Verify down = %v", err)
	}
	if _, _, err := w.s.AuthenticateRunner(ctx, rt.Raw); !errors.Is(err, errDown) {
		t.Errorf("AuthenticateRunner with Verify down = %v", err)
	}
	w.f.fail = "Issue"
	if _, err := w.s.WorkerToken(ctx, worker, 7); !errors.Is(err, errDown) {
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

// verifying is authn.Verifying over fixed keys, or err.
type verifying struct {
	keys []jose.JSONWebKey
	err  error
}

func (v verifying) VerifyingKeys(context.Context) (string, []jose.JSONWebKey, error) {
	return "https://iw.example.com", v.keys, v.err
}

// RunTokenKeys answers each verifying key's public point, with the run
// token's issuer and audience, and refuses a key that is not P-256.
func TestUnitRunTokenKeys(t *testing.T) {
	ctx := t.Context()
	f := newWorld(t).f
	service := func(v authn.Verifying) *authn.Service {
		return authn.New(authn.Deps{Sessions: f, Verifying: v, Now: time.Now})
	}
	if _, err := service(nil).RunTokenKeys(ctx); !errors.Is(err, authn.ErrRunTokenKeys) {
		t.Errorf("RunTokenKeys with no keys = %v", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "k1", Algorithm: "ES256", Use: "sig"}
	got, err := service(verifying{keys: []jose.JSONWebKey{jwk}}).RunTokenKeys(ctx)
	if err != nil || got.Issuer != "https://iw.example.com" || got.Audience != accessapi.RunnerAudience || len(got.Keys) != 1 {
		t.Fatalf("RunTokenKeys = %+v, %v", got, err)
	}
	want, err := json.Marshal(jwk)
	if err != nil {
		t.Fatal(err)
	}
	var point struct {
		X string `json:"x"`
		Y string `json:"y"`
	}
	if err := json.Unmarshal(want, &point); err != nil {
		t.Fatal(err)
	}
	if k := got.Keys[0]; k.ID != "k1" || k.X != point.X || k.Y != point.Y {
		t.Errorf("key = %+v, want the JWK's point %+v", k, point)
	}
	other, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []jose.JSONWebKey{{Key: &other.PublicKey, KeyID: "p384"}, {Key: []byte("hmac"), KeyID: "hs"}} {
		_, err := service(verifying{keys: []jose.JSONWebKey{bad}}).RunTokenKeys(ctx)
		if !errors.Is(err, authn.ErrRunTokenKeys) {
			t.Errorf("RunTokenKeys of %s = %v", bad.KeyID, err)
		}
	}
	down := errors.New("store down")
	if _, err := service(verifying{err: down}).RunTokenKeys(ctx); !errors.Is(err, down) {
		t.Errorf("RunTokenKeys = %v, want the store's error", err)
	}
}
