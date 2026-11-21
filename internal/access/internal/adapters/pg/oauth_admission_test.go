package pg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestAccOAuthConsentExpiresWhileAccountFenceIsHeld(t *testing.T) {
	x := seed(t, pool(t), "oauth-consent-expiry-fence")
	admit(t, x, x.member)
	ctx := t.Context()
	ss := x.webSession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	r, _ := oauthRequest(t, x)
	r.ID = kernel.NewULID()
	now := time.Now()
	r.ExpiresAt = now.Add(500 * time.Millisecond)
	if err := x.s.CreateRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	_, r.CodeHash = session.NewSecret()
	r.CodeExpiresAt = now.Add(time.Minute)
	holder, err := x.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var account kernel.AccountID
	if err := holder.QueryRow(ctx, `SELECT id FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, x.acct).Scan(&account); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- x.s.Approve(ctx, ss.Principal(), r, now) }()
	// The request's wall-clock deadline elapses while the account lock is
	// unavailable; neither its admission nor its code can be committed late.
	select {
	case err := <-done:
		t.Fatalf("consent did not wait for the account fence: %v", err)
	case <-time.After(time.Until(r.ExpiresAt) + 20*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("expired consent issued a code after waiting: %v", err)
	}
	got, err := x.s.Request(ctx, r.ID)
	if err != nil || got.Decided || len(got.CodeHash) != 0 {
		t.Fatalf("expired request was consumed: decided=%t codeStored=%t err=%v", got.Decided, len(got.CodeHash) > 0, err)
	}
}
