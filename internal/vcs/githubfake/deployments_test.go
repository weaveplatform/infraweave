package githubfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
)

type deploymentFixture struct {
	f       *githubfake.Fake
	handler http.Handler
	jwt     string
	install int64
	sha     string
}

func newDeploymentFixture(t *testing.T) deploymentFixture {
	t.Helper()
	f := githubfake.New()
	app := f.AddApp("deployments", "acme")
	id := f.Install(app.ID, "acme", "Organization", "infra", "web")
	sha := f.Repo("acme", "infra").Commit("main", "first", map[string]string{"a.tf": "test"})
	f.Repo("acme", "web").Commit("main", "other", map[string]string{"a.tf": "test"})
	key, err := jwt.ParseRSAPrivateKeyFromPEM(app.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": strconv.FormatInt(app.ID, 10), "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return deploymentFixture{f: f, handler: f.Handler(), jwt: signed, install: id, sha: sha}
}

func (f deploymentFixture) send(t *testing.T, method, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), method, "/api/v3"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+auth)
	r.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(w, r)
	return w
}

func (f deploymentFixture) token(t *testing.T, permission string) string {
	t.Helper()
	w := f.send(t, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", f.install), f.jwt,
		`{"repositories":["infra","web"],"permissions":`+permission+`}`)
	var out struct {
		Token string `json:"token"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Token == "" {
		t.Fatal("installation token refused", w.Code, w.Body.String())
	}
	return out.Token
}

func TestUnitDeploymentNativeRoutes(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	token := f.token(t, `{"deployments":"write"}`)
	for _, environment := range []string{"prod", "qa", "prod"} {
		body := fmt.Sprintf(
			`{"ref":%q,"task":"custom task","environment":%q,"auto_merge":false,"required_contexts":[],"production_environment":true,"payload":{"runId":"run-1"}}`,
			f.sha,
			environment,
		)
		if w := f.send(t, http.MethodPost, "/repos/acme/infra/deployments", token, body); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	records := f.f.Deployments("acme", "infra")
	first := records[0]
	if len(records) != 3 || first.SHA != f.sha || first.Ref != f.sha || first.AutoMerge == nil || *first.AutoMerge ||
		first.RequiredContexts == nil || len(first.RequiredContexts) != 0 || first.ProductionEnvironment == nil || !*first.ProductionEnvironment ||
		string(first.Payload) != `{"runId":"run-1"}` {
		t.Fatal("create recorder lost native fields or explicit context bypass")
	}
	for _, state := range []string{"pending", "queued", "success"} {
		if w := f.send(t, http.MethodPost, fmt.Sprintf("/repos/acme/infra/deployments/%d/statuses", first.ID), token, `{"state":"`+state+`"}`); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := f.send(t, http.MethodGet, "/repos/acme/infra/deployments?environment=prod&task=custom+task&sha="+f.sha+"&per_page=1", token, "")
	var page []struct {
		ID int64 `json:"id"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page) != 1 || page[0].ID != records[2].ID || !strings.Contains(w.Header().Get("Link"), "page=2") {
		t.Fatal("native list filtering or newest-first pagination changed", w.Code, w.Body.String())
	}
	w = f.send(t, http.MethodGet, fmt.Sprintf("/repos/acme/infra/deployments/%d/statuses?per_page=1", first.ID), token, "")
	var statuses []struct {
		State string `json:"state"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &statuses) != nil || len(statuses) != 1 || statuses[0].State != "success" || w.Header().Get("Link") == "" {
		t.Fatal("native status list is not newest first and paginated")
	}
	records = f.f.Deployments("acme", "infra")
	records[0].Payload[0], *records[0].AutoMerge, *records[0].ProductionEnvironment = 'x', true, false
	records[0].Statuses[0].State = "changed"
	writes := f.f.DeploymentWrites("acme", "infra")
	if len(writes) != 3 || writes[0].State != "pending" || writes[2].State != "success" {
		t.Fatal("status history order changed")
	}
	writes[0].State = "changed"
	fresh := f.f.Deployments("acme", "infra")[0]
	if fresh.Payload[0] != '{' || *fresh.AutoMerge || !*fresh.ProductionEnvironment || fresh.Statuses[0].State != "pending" ||
		f.f.DeploymentWrites("acme", "infra")[0].State != "pending" {
		t.Fatal("recorder snapshot aliases mutable state")
	}
}

func TestUnitDeploymentPermissionsAndValidation(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	write := f.token(t, `{"deployments":"write"}`)
	read := f.token(t, `{"deployments":"read"}`)
	metadata := f.token(t, `{"metadata":"read"}`)
	path := "/repos/acme/infra/deployments"
	for _, tc := range []struct {
		method, path, token, body string
		want                      int
	}{
		{http.MethodGet, path, "wrong", "", 401},
		{http.MethodGet, path, metadata, "", 403},
		{http.MethodPost, path, read, `{"ref":"main"}`, 403},
		{http.MethodGet, path, read, "", 200},
		{http.MethodGet, "/repos/hidden/infra/deployments", write, "", 404},
		{http.MethodGet, "/repos/acme/missing/deployments", write, "", 404},
		{http.MethodGet, "/repos/acme/infra/commits/main", write, "", 403},
		{http.MethodPost, path, write, `{`, 422},
		{http.MethodPost, path, write, `{}`, 422},
		{http.MethodPost, path, write, `{"ref":"gone"}`, 422},
		{http.MethodPost, path + "/999/statuses", write, `{"state":"success"}`, 404},
		{http.MethodGet, path + "/bad/statuses", read, "", 404},
		{http.MethodPost, path + "/999/statuses", write, `{"state":"invented"}`, 422},
	} {
		if w := f.send(t, tc.method, tc.path, tc.token, tc.body); w.Code != tc.want {
			t.Fatalf("%s %s=%d want%d: %s", tc.method, tc.path, w.Code, tc.want, w.Body)
		}
	}
	if w := f.send(t, http.MethodPost, path, write, `{"ref":"main","environment":"qa"}`); w.Code != 201 || !strings.Contains(w.Body.String(), `"production_environment":false`) {
		t.Fatal("native non-production default changed")
	}
	id := f.f.Deployments("acme", "infra")[0].ID
	if w := f.send(t, http.MethodPost, fmt.Sprintf("/repos/acme/web/deployments/%d/statuses", id), write, `{"state":"success"}`); w.Code != 404 {
		t.Fatal("another repository mutated a deployment")
	}
}

func TestUnitDeploymentLostResponseAndConcurrency(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	token := f.token(t, `{"deployments":"write"}`)
	f.f.FailNextDeploymentCreate()
	path := "/repos/acme/infra/deployments"
	if w := f.send(t, http.MethodPost, path, token, `{"ref":"main","payload":{"runId":"one"}}`); w.Code != 502 {
		t.Fatal("create response was not lost")
	}
	if w := f.send(t, http.MethodGet, path, token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"runId":"one"`) {
		t.Fatal("lost create is not recoverable")
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			if w := f.send(t, http.MethodPost, path, token, `{"ref":"main"}`); w.Code != 201 {
				t.Error("concurrent create refused", w.Code)
			}
		})
	}
	workers.Wait()
	records := f.f.Deployments("acme", "infra")
	ids := map[int64]bool{}
	for _, record := range records {
		ids[record.ID] = true
	}
	if len(records) != 13 || len(ids) != 13 {
		t.Fatal("concurrent native deployment identities collided")
	}
}
