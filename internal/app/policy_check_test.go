package app

import (
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	expressionsassembly "github.com/weaveplatform/infraweave/internal/expressions/assembly"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/sql/sqltest"
	"github.com/weaveplatform/infraweave/pkg/sdk"
)

func TestAccPolicyCheckUnavailableSurface(t *testing.T) {
	ctx := t.Context()
	db := sqltest.Open(t)
	c := config.Defaults()
	d, err := newDomains(c, db, testKeyring(t, db.Primary()), nil, nil, testEvents(t, db.Primary()), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Features.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	org, account, key := kernel.NewOrganizationID(), kernel.NewAccountID(), kernel.NewULID()
	secret := "iwk_" + strings.Repeat("s", 43)
	hash := sha256.Sum256([]byte(secret))
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations (id,slug,name) VALUES ($1,'check','Check')`, []any{org}},
		{`INSERT INTO accounts (organization_id,id,slug,name) VALUES ($1,$2,'check','Check')`, []any{org, account}},
		{`INSERT INTO spaces (account_id,id,slug,name) VALUES ($1,$1,'check','Check')`, []any{account}},
		{`INSERT INTO space_closure (account_id,ancestor_id,descendant_id,depth) VALUES ($1,$1,$1,0)`, []any{account}},
		{`INSERT INTO api_keys (account_id,id,name,type,secret_hash,creator) VALUES ($1,$2,'check','SECRET',$3,'test')`, []any{account, key, hash[:]}},
	} {
		if _, err := db.Primary().Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, surfaces := range [][]expr.Surface{nil, {{Name: "policy"}}} {
		registry, err := expr.NewRegistry(c.Expressions, surfaces...)
		if err != nil {
			t.Fatal(err)
		}
		d.Expressions = expressionsassembly.New(registry)
		server := httptest.NewServer(API(discard(), c.Server, memory(c.Server), d, Platform{}, Blobs{}))
		t.Cleanup(server.Close)
		client, err := sdk.New(server.URL, sdk.WithRetry(sdk.NoRetry))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(ctx, http.MethodPost, BaseURL+"/auth/token", map[string]string{"id": key.String(), "secret": secret})
		if err != nil {
			t.Fatal(err)
		}
		var token struct {
			JWT string `json:"jwt"`
		}
		if err := json.Unmarshal(response.Body, &token); err != nil || token.JWT == "" {
			t.Fatalf("real authentication produced no token: %v", err)
		}
		client, err = sdk.New(server.URL, sdk.WithToken(token.JWT), sdk.WithRetry(sdk.NoRetry))
		if err != nil {
			t.Fatal(err)
		}
		response, err = client.Do(ctx, http.MethodPost, BaseURL+"/policies/check", map[string]string{"type": "PLAN", "body": "package infraweave\n"})
		if response == nil || err == nil || response.Status != 503 || !strings.Contains(string(response.Body), "service-unavailable") || response.Header.Get("Retry-After") == "" {
			t.Fatalf("missing surface did not fail through shared HTTP controls: %+v %v", response, err)
		}
		server.Close()
	}
}
