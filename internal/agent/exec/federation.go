package exec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/workspace"
)

// Federation reissues the OIDC federation token of the phase a run is in;
// *client.Client is one.
type Federation interface {
	OIDCToken(ctx context.Context, token, run string) (wire.OIDCToken, error)
}

// Federation token refreshes: the runner asks for a new token
// refreshLead before the one it holds expires, or, when the token lives
// no longer than that, halfway to its expiry, so a short-lived token is
// not asked for without pause. A failed call is retried from
// refreshWait, doubling up to refreshWaitMax, until the held token
// expires.
//
// ref: decision 0006 § "OIDC federation token"
const (
	refreshLead    = 15 * time.Minute
	refreshWait    = time.Second
	refreshWaitMax = time.Minute
)

// refreshIn is how long after now a token that expires at expires is
// refreshed.
func refreshIn(expires, now time.Time) time.Duration {
	left := expires.Sub(now)
	if left > refreshLead {
		return left - refreshLead
	}
	return max(left/2, 0)
}

// refresher keeps the federation token file of a run's phases fresh: at
// each refresh it asks the worker API for a new token with the run's
// token and hands it to replace, which masks it and replaces the file. It
// never fails the phase, which may not need federation again: a failure
// is logged, without the token, and retried until the held token
// expires, after which the refresher ends and the phase goes on with the
// file it has.
type refresher struct {
	api     Federation
	now     func() time.Time
	log     *slog.Logger
	run     string
	token   string
	replace func(raw string) error
}

// keep refreshes the token that expires at expires, and each token after
// it, until ctx ends or a token expires unrefreshed.
func (f *refresher) keep(ctx context.Context, expires time.Time) {
	for {
		if !f.sleep(ctx, refreshIn(expires, f.now())) {
			return
		}
		next, ok := f.renew(ctx, expires)
		if !ok {
			return
		}
		expires = next
	}
}

// renew asks for a new token and replaces the file with it, trying again
// as the constants above say, and answers its expiry; false when ctx
// ended or the held token, which expires at expires, expired first.
func (f *refresher) renew(ctx context.Context, expires time.Time) (time.Time, bool) {
	wait := refreshWait
	for {
		tok, err := f.api.OIDCToken(ctx, f.token, f.run)
		if err == nil {
			err = f.replace(tok.Token)
		}
		if err == nil {
			next := time.Unix(tok.ExpiresAt, 0)
			f.log.InfoContext(ctx, "federation token refreshed", "run", f.run, "expires", next.UTC())
			return next, true
		}
		if ctx.Err() != nil {
			return time.Time{}, false
		}
		if !f.now().Add(wait).Before(expires) {
			f.log.WarnContext(ctx, "federation token not refreshed before it expires", "run", f.run,
				"expires", expires.UTC(), "error", err)
			return time.Time{}, false
		}
		f.log.WarnContext(ctx, "federation token not refreshed", "run", f.run, "error", err, "retry", wait)
		if !f.sleep(ctx, wait) {
			return time.Time{}, false
		}
		wait = min(wait*2, refreshWaitMax)
	}
}

// sleep waits d, and reports false when ctx ends first.
func (f *refresher) sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// refresh starts keeping the handoff's federation token fresh, once the
// phase's file is written, when the handoff carries the token's expiry
// and the run has a Federation to ask; close stops it before the
// workspace is handed back.
//
// ref: decision 0006 § "OIDC federation token"
func (r *run) refresh(ctx context.Context) {
	t, exp := r.h.Secrets.OidcToken, r.h.Secrets.OidcTokenExpiresAt
	if r.d.Federation == nil || t == nil || *t == "" || exp == nil || r.refreshed != nil {
		return
	}
	now := r.d.Now
	if now == nil {
		now = time.Now
	}
	f := &refresher{
		api: r.d.Federation, now: now, log: r.d.Log, run: r.h.Run, token: r.h.RunToken,
		replace: r.replaceOIDCToken,
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.refreshed = func() {
		cancel()
		<-done
	}
	go func() {
		defer close(done)
		f.keep(ctx, time.Unix(*exp, 0))
	}()
}

// replaceOIDCToken masks raw, a refreshed federation token, in the log
// and forbids it in the saved workspace, and only then puts it at
// workspace.OIDCTokenFile: written whole to workspace.OIDCTokenNext and
// renamed over the file, so a reader of the file sees the old token or
// the new one, never a part. INFRAWEAVE_OIDC_TOKEN keeps the phase's
// first token.
//
// ref: decision 0006 § "OIDC federation token"
func (r *run) replaceOIDCToken(raw string) error {
	r.log.Add([]byte(raw))
	r.oidc.Lock()
	r.oidc.tokens = append(r.oidc.tokens, raw)
	r.oidc.Unlock()
	root, err := os.OpenRoot(r.d.Workspace)
	if err != nil {
		return fmt.Errorf("open the workspace: %w", err)
	}
	defer root.Close()
	if err := write(root, workspace.OIDCTokenNext, []byte(raw), oidcTokenFileMode); err != nil {
		return fmt.Errorf("write the refreshed federation token: %w", err)
	}
	if err := root.Rename(workspace.OIDCTokenNext, workspace.OIDCTokenFile); err != nil {
		return fmt.Errorf("replace the federation token: %w", err)
	}
	return nil
}

// oidcTokens are the federation tokens the run's phases hold: the
// handoff's and each refresh's.
func (r *run) oidcTokens() [][]byte {
	var out [][]byte
	if t := r.h.Secrets.OidcToken; t != nil {
		out = append(out, []byte(*t))
	}
	r.oidc.Lock()
	defer r.oidc.Unlock()
	for _, t := range r.oidc.tokens {
		out = append(out, []byte(t))
	}
	return out
}
