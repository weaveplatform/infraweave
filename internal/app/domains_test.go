package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
)

// TestAccJobEntitlements checks the queue's entitlement check as the
// scheduler runs it: a job's account names its organization, whose tier
// decides with billing on; a job of no account still meets features.disabled.
func TestAccJobEntitlements(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	c := config.Defaults()
	c.Features.Disabled = []string{"scans"}
	d, err := newDomains(c, db, testKeyring(t, db.Primary()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	org, acct := kernel.NewOrganizationID(), kernel.NewAccountID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, slug, name) VALUES ($1, 'o', 'o')`, []any{org}},
		{`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'o', 'o')`, []any{org, acct}},
		{`UPDATE billing SET enabled = true, default_tier = 'Free'`, nil},
	} {
		if _, err := db.Primary().Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Features.Entitlements().CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		account kernel.ULID
		feature string
		want    bool
	}{
		{acct.ULID, "stacks", true},
		{acct.ULID, "policies", false},
		{kernel.NewULID(), "policies", true},
		{kernel.NewULID(), "scans", false},
	} {
		if ok, err := d.Allowed(ctx, c.account, c.feature); err != nil || ok != c.want {
			t.Errorf("Allowed(%v, %s) = %t, %v; want %t", c.account, c.feature, ok, err, c.want)
		}
	}
	db.Close()
	if _, err := d.Allowed(context.Background(), acct.ULID, "stacks"); err == nil {
		t.Error("a closed database answered")
	}
}

// TestAccSeatRace checks the race for an organization's last seat under
// PostgreSQL, through both domains as the server wires them: two keys' first
// uses race for it, and exactly one exchange is admitted while the other
// answers 403 limit-reached, since the count locks the organization's
// entitlement row before counting.
//
// ref: decision 0021
func TestAccSeatRace(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	c := config.Defaults()
	c.Server.PublicURL = "http://iw.test"
	d, err := newDomains(c, db, testKeyring(t, db.Primary()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	org, acct, member := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewMemberID()
	type key struct {
		id     kernel.ULID
		secret string
	}
	keys := []key{{id: kernel.NewULID()}, {id: kernel.NewULID()}}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id, slug, name) VALUES ($1, 'o', 'o')`, []any{org}},
		{
			`INSERT INTO accounts (organization_id, id, slug, name) VALUES ($1, $2, 'o', 'o')`,
			[]any{org, acct},
		},
		{`INSERT INTO members (organization_id, id, login, name, email, status)
			VALUES ($1, $2, 'alice', 'Alice', 'alice@example.com', 'active')`, []any{org, member}},
		{
			`INSERT INTO account_users (account_id, organization_id, member_id) VALUES ($1, $2, $3)`,
			[]any{acct, org, member},
		},
		{`UPDATE billing SET enabled = true, default_tier = 'Free'`, nil},
	}
	for _, s := range stmts {
		if _, err := db.Primary().Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := range keys {
		raw := make([]byte, 32)
		_, _ = rand.Read(raw)
		keys[i].secret = "iwk_" + base64.RawURLEncoding.EncodeToString(raw)
		sum := sha256.Sum256([]byte(keys[i].secret))
		if _, err := db.Primary().Exec(ctx, `INSERT INTO api_keys
			(account_id, id, name, type, secret_hash, creator)
			VALUES ($1, $2, 'ci', 'SECRET', $3, 'test')`, acct, keys[i].id, sum[:]); err != nil {
			t.Fatal(err)
		}
	}
	// Free holds 2 seats; alice takes one, so one key's first use may take
	// the last.
	if err := d.Features.Entitlements().CreateOrganization(ctx, org); err != nil {
		t.Fatal(err)
	}
	h := API(discard(), c.Server, memory(c.Server), d, Platform{}, Blobs{})
	codes := make([]int, len(keys))
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Go(func() {
			body := fmt.Sprintf(`{"id":%q,"secret":%q}`, k.id, k.secret)
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/auth/token",
				strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			codes[i] = rec.Code
			if rec.Code == http.StatusForbidden &&
				!strings.Contains(rec.Body.String(), `"limit":"seats"`) {
				t.Errorf("the refused exchange: %s", rec.Body)
			}
		})
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{http.StatusOK, http.StatusForbidden}) {
		t.Errorf("exchanges answered %v, want one 200 and one 403", codes)
	}
}
