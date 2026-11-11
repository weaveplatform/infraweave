package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var now = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

// minter mints a token with id, or fails with err, and records the
// tokens it is asked to revoke, failing those with revokeErr.
type minter struct {
	source

	id        string
	err       error
	revokeErr error
	revoked   *[]string
	named     *string
}

func (m minter) Mint(
	_ context.Context,
	_ port.Repository,
	name string,
	expires time.Time,
) (port.Credential, error) {
	if m.named != nil {
		*m.named = name
	}
	return port.Credential{
		Username: "minted", Token: []byte("narrow"), ID: m.id, ExpiresAt: expires,
	}, m.err
}

func (m minter) Revoke(_ context.Context, repo port.Repository, id string) error {
	if repo.Integration == nil {
		return errOffline
	}
	*m.revoked = append(*m.revoked, id)
	return m.revokeErr
}

// lender is a minter that also lends the integration's own token.
type lender struct{ minter }

func (lender) Lend(in port.Integration) port.Credential {
	return port.Credential{Username: "own", Token: in.Credential}
}

// tokens keeps tokens in memory, failing each call with its error.
type tokens struct {
	kept                     *[]Token
	addErr, listErr, rmError error
}

func (t tokens) Add(_ context.Context, tok Token) error {
	if t.addErr != nil {
		return t.addErr
	}
	*t.kept = append(*t.kept, tok)
	return nil
}

func (t tokens) List(context.Context, kernel.AccountID, kernel.ULID) ([]Token, error) {
	return *t.kept, t.listErr
}

func (t tokens) Remove(_ context.Context, _ kernel.AccountID, _ kernel.ULID, id string) error {
	if t.rmError != nil {
		return t.rmError
	}
	for i, tok := range *t.kept {
		if tok.ID == id {
			*t.kept = append((*t.kept)[:i], (*t.kept)[i+1:]...)
			break
		}
	}
	return nil
}

// jobs records what is enqueued.
type jobs struct {
	kinds   *[]string
	payload *any
}

func (j jobs) EnqueueTx(
	_ context.Context,
	k queue.Kind,
	payload any,
	_ ...queue.Option,
) (int64, error) {
	*j.kinds = append(*j.kinds, k.Name)
	*j.payload = payload
	return 1, nil
}

// credentialFixture is a service over one GITLAB stack, its integration,
// a provider src and the kept tokens, logging into logged.
type credentialFixture struct {
	svc     *Service
	kept    []Token
	revoked []string
	logged  bytes.Buffer
	acct    kernel.AccountID
	stack   kernel.ULID
	run     kernel.ULID
	in      port.Integration
}

func newCredentialFixture(
	t *testing.T,
	src func(f *credentialFixture) port.Source,
) *credentialFixture {
	t.Helper()
	f := &credentialFixture{
		acct: kernel.NewAccountID(), stack: kernel.NewULID(), run: kernel.NewULID(),
		in: port.Integration{ID: kernel.NewULID(), Provider: "GITLAB", Credential: []byte("wide")},
	}
	f.svc = New(Deps{
		Stacks:       stacks{settings: settings("GITLAB", nil)},
		Providers:    map[string]port.Source{"GITLAB": src(f)},
		Integrations: integrations{in: f.in, found: true},
		Tokens:       tokens{kept: &f.kept},
		Now:          func() time.Time { return now },
		Log:          slog.New(slog.NewTextHandler(&f.logged, nil)),
	})
	return f
}

func (f *credentialFixture) credential(t *testing.T) (*vcsapi.Credential, error) {
	t.Helper()
	return f.svc.Credential(t.Context(), f.acct, f.stack, f.run)
}

// TestUnitCredentialMinted checks that a minted token reads the run's
// repository alone, is kept for Revoke under the run's name and the
// integration's ID, and expires after TokenLife; that one with no ID is
// not kept; and that a token the Tokens cannot keep is revoked at once.
func TestUnitCredentialMinted(t *testing.T) {
	var named string
	f := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return minter{id: "7", revoked: &f.revoked, named: &named}
	})
	c, err := f.credential(t)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Narrowed || string(c.Token) != "narrow" || c.Username != "minted" ||
		!c.ExpiresAt.Equal(now.Add(TokenLife)) || named != "infraweave-run-"+f.run.String() {
		t.Errorf("credential = %+v named %q", c, named)
	}
	want := Token{
		Account: f.acct, Run: f.run, ID: "7", Stack: f.stack, Integration: f.in.ID,
		Provider: "GITLAB", Namespace: "acme", Repository: "infra", ExpiresAt: now.Add(TokenLife),
	}
	if len(f.kept) != 1 || f.kept[0] != want {
		t.Errorf("kept %+v, want %+v", f.kept, want)
	}

	expiring := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return minter{revoked: &f.revoked}
	})
	if c, err := expiring.credential(t); err != nil || !c.Narrowed || len(expiring.kept) != 0 {
		t.Errorf("a token that only expires = %+v, %v, kept %+v", c, err, expiring.kept)
	}

	unkept := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return minter{id: "8", revoked: &f.revoked}
	})
	unkept.svc.tokens = tokens{kept: &unkept.kept, addErr: errOffline}
	if _, err := unkept.credential(t); !errors.Is(err, errOffline) ||
		len(unkept.revoked) != 1 || unkept.revoked[0] != "8" {
		t.Errorf("an unkept token = %v, revoked %v", err, unkept.revoked)
	}
}

// TestUnitCredentialFallback checks that an integration that may not
// mint lends its own token, which the log notes without the token; that
// a provider that neither mints nor lends fails; that another mint
// failure fails; and that raw Git needs no credential.
func TestUnitCredentialFallback(t *testing.T) {
	cannot := fmt.Errorf("gitlab: below Maintainer: %w", port.ErrCannotMint)
	f := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return lender{minter{err: cannot, revoked: &f.revoked}}
	})
	c, err := f.credential(t)
	if err != nil {
		t.Fatal(err)
	}
	if c.Narrowed || string(c.Token) != "wide" || c.Username != "own" || c.ExpiresAt != nil {
		t.Errorf("fallback = %+v", c)
	}
	if log := f.logged.String(); !strings.Contains(log, "own token") ||
		!strings.Contains(log, f.run.String()) || strings.Contains(log, "wide") {
		t.Errorf("log = %q", log)
	}
	if s := fmt.Sprintf("%v %#v %s", *c, *c, *c); strings.Contains(s, "wide") {
		t.Errorf("formatted credential carries its token: %s", s)
	}
	var out bytes.Buffer
	slog.New(slog.NewJSONHandler(&out, nil)).Info("c", slog.Any("credential", *c))
	if strings.Contains(out.String(), "wide") ||
		!strings.Contains(out.String(), `"username":"own"`) {
		t.Errorf("logged credential = %s", out.String())
	}

	for name, src := range map[string]func(f *credentialFixture) port.Source{
		"no lender":    func(f *credentialFixture) port.Source { return minter{err: cannot, revoked: &f.revoked} },
		"offline mint": func(f *credentialFixture) port.Source { return lender{minter{err: errOffline, revoked: &f.revoked}} },
		"neither":      func(*credentialFixture) port.Source { return source{} },
	} {
		if c, err := newCredentialFixture(t, src).credential(t); err == nil {
			t.Errorf("%s = %+v", name, c)
		}
	}

	git := newCredentialFixture(t, func(*credentialFixture) port.Source { return source{} })
	git.svc.integrations = integrations{}
	if c, err := git.credential(t); c != nil || err != nil {
		t.Errorf("raw Git = %+v, %v", c, err)
	}
	gone := newCredentialFixture(t, func(*credentialFixture) port.Source { return source{} })
	gone.svc.stacks = stacks{err: errOffline}
	if _, err := gone.credential(t); !errors.Is(err, errOffline) {
		t.Errorf("a failing stack read = %v", err)
	}
}

// TestUnitRelease checks that Release enqueues one Revoke job for the
// run, and that the job revokes each kept token through its integration
// and forgets it, forgets one whose integration is gone, and fails on a
// payload that does not decode, a failing read, a failing revoke and a
// failing forget.
func TestUnitRelease(t *testing.T) {
	f := newCredentialFixture(t, func(f *credentialFixture) port.Source {
		return minter{id: "7", revoked: &f.revoked}
	})
	var kinds []string
	var payload any
	f.svc.jobs = jobs{kinds: &kinds, payload: &payload}
	if err := f.svc.Release(t.Context(), f.acct, f.run); err != nil || len(kinds) != 1 ||
		kinds[0] != Revoke.Name {
		t.Fatalf("release = %v, enqueued %v", err, kinds)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	job := queue.Job{Payload: body}

	tok := Token{
		Account: f.acct, Run: f.run, Integration: f.in.ID, Provider: "GITLAB",
		Namespace: "acme", Repository: "infra",
	}
	for _, id := range []string{"7", "8"} {
		tok.ID = id
		f.kept = append(f.kept, tok)
	}
	if err := f.svc.Revoked(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if len(f.kept) != 0 || fmt.Sprint(f.revoked) != "[7 8]" {
		t.Errorf("kept %+v, revoked %v", f.kept, f.revoked)
	}
	if err := f.svc.Revoked(t.Context(), job); err != nil || len(f.revoked) != 2 {
		t.Errorf("a second run = %v, revoked %v", err, f.revoked)
	}

	f.kept = append(f.kept, tok)
	f.svc.integrations = integrations{}
	if err := f.svc.Revoked(
		t.Context(),
		job,
	); err != nil || len(f.kept) != 0 ||
		len(f.revoked) != 2 {
		t.Errorf("a deleted integration's token = %v, kept %+v, revoked %v", err, f.kept, f.revoked)
	}

	if err := f.svc.Revoked(t.Context(), queue.Job{Payload: []byte("{")}); !permanent(err) {
		t.Errorf("a payload that does not decode = %v", err)
	}
	f.kept = append(f.kept, tok)
	for name, set := range map[string]func(){
		"integration": func() { f.svc.integrations = integrations{err: errOffline} },
		"revoke": func() {
			f.svc.integrations = integrations{in: f.in, found: true}
			f.svc.providers["GITLAB"] = minter{revoked: &f.revoked, revokeErr: errOffline}
		},
		"forget": func() {
			f.svc.providers["GITLAB"] = minter{revoked: &f.revoked}
			f.svc.tokens = tokens{kept: &f.kept, rmError: errOffline}
		},
		"list": func() { f.svc.tokens = tokens{kept: &f.kept, listErr: errOffline} },
	} {
		set()
		if err := f.svc.Revoked(t.Context(), job); !errors.Is(err, errOffline) {
			t.Errorf("a failing %s = %v", name, err)
		}
	}
}
