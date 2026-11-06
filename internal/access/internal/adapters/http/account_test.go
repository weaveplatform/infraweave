package http

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitAccountReads checks that each read answers the caller's account,
// and that each refuses a request without a credential.
func TestUnitAccountReads(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	for path, want := range map[string]string{
		"/id":   w.acct.String(),
		"/name": "Acme",
		"/type": "ORG",
		"/default-private-worker-pool-runner-image": account.DefaultRunnerImage,
	} {
		rec := w.do(t, http.MethodGet, path, me, "")
		var got string
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK ||
			got != want {
			t.Errorf("%s = %d %s, want %q", path, rec.Code, rec.Body, want)
		}
		wantUnauthenticated(t, w.do(t, http.MethodGet, path, "", ""), true)
	}
}

// TestUnitAccountNameStore checks that name passes the store's error on:
// 503 while the database is down, and 500 for an account the store lacks,
// which no live credential names.
func TestUnitAccountNameStore(t *testing.T) {
	w := newWorld(t)
	me := "Bearer " + w.exchange(t).Jwt
	w.do(t, http.MethodGet, "/viewer", me, "")
	w.st.down = true
	if rec := w.do(t, http.MethodGet, "/name", me, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("down: %d %s", rec.Code, rec.Body)
	}
	w.st.down = false
	w.st.accounts = map[kernel.AccountID]account.Account{}
	if rec := w.do(t, http.MethodGet, "/name", me, ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("no account: %d %s", rec.Code, rec.Body)
	}
}
