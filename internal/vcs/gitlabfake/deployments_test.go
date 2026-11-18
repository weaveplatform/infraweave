package gitlabfake_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

type deploymentFixture struct {
	f                     *gitlabfake.Fake
	handler               http.Handler
	write, read, reporter string
}

func newDeploymentFixture(t *testing.T) deploymentFixture {
	t.Helper()
	f := gitlabfake.New()
	write, read, reporter := f.AddToken("dev", "api"), f.AddToken("dev", "read_api"), f.AddToken("reader", "api")
	f.AddMember("acme/platform", "infra", "dev", gitlabfake.Developer)
	f.AddMember("acme/platform", "infra", "reader", gitlabfake.Reporter)
	f.AddMember("acme", "web", "dev", gitlabfake.Developer)
	return deploymentFixture{f: f, handler: f.Handler(), write: write, read: read, reporter: reporter}
}

func (f deploymentFixture) send(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), method, "/api/v4/projects/"+path, strings.NewReader(body))
	r.Header.Set("PRIVATE-TOKEN", token)
	r.Header.Set("Content-Type", "application/json")
	f.handler.ServeHTTP(w, r)
	return w
}

const deploymentProject = "acme%2Fplatform%2Finfra"

func TestUnitDeploymentNativeRoutes(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	for _, name := range []string{"infraweave/infra", "prod", "qa"} {
		if w := f.send(t, http.MethodPost, deploymentProject+"/environments", f.write, fmt.Sprintf(`{"name":%q}`, name)); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := f.send(
		t,
		http.MethodGet,
		deploymentProject+"/environments?name=prod",
		f.read,
		"",
	); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"prod"`) ||
		strings.Contains(w.Body.String(), `"name":"qa"`) {
		t.Fatal("environment name is not an exact native filter")
	}
	w := f.send(t, http.MethodGet, deploymentProject+"/environments?per_page=1", f.read, "")
	if w.Code != 200 || w.Header().Get("X-Next-Page") != "2" || w.Header().Get("X-Total") != "3" {
		t.Fatal("environments do not use native pagination")
	}
	for _, sha := range []string{"commit-one", "commit-two", "commit-three"} {
		body := fmt.Sprintf(`{"environment":"prod","sha":%q,"ref":"main","tag":false,"status":"running"}`, sha)
		if w := f.send(t, http.MethodPost, deploymentProject+"/deployments", f.write, body); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	records := f.f.Deployments("acme/platform", "infra")
	first := records[0]
	if len(records) != 3 || first.Environment.Name != "prod" || first.SHA != "commit-one" || first.Tag {
		t.Fatal("native create shape changed")
	}
	for _, state := range []string{"success", "failed", "canceled"} {
		if w := f.send(t, http.MethodPut, fmt.Sprintf("%s/deployments/%d", deploymentProject, first.ID), f.write, `{"status":"`+state+`"}`); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// These fields are returned by GitLab, but are not native list filters.
	w = f.send(t, http.MethodGet, deploymentProject+"/deployments?environment=prod&ref=wrong&sha=wrong&per_page=1", f.read, "")
	var page []struct {
		ID int64 `json:"id"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page) != 1 || page[0].ID != records[0].ID || w.Header().Get("X-Total") != "3" ||
		w.Header().Get("X-Next-Page") != "2" {
		t.Fatal("native deployment pagination added invented ref/SHA filters", w.Body.String())
	}
	w = f.send(t, http.MethodGet, deploymentProject+"/deployments?environment=other", f.read, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal("environment filtering changed")
	}
	writes := f.f.DeploymentWrites("acme/platform", "infra")
	if len(writes) != 6 || writes[0].State != "running" || writes[3].State != "success" || writes[5].State != "canceled" {
		t.Fatal("native mutation order changed")
	}
	writes[0].State = "changed"
	records = f.f.Deployments("acme/platform", "infra")
	records[0].Environment.Name, records[0].States[0] = "changed", "changed"
	environments := f.f.Environments("acme/platform", "infra")
	environments[0].Name = "changed"
	if got := f.f.Deployments("acme/platform", "infra")[0]; got.Environment.Name != "prod" || got.States[0] != "running" || got.Status != "canceled" ||
		f.f.DeploymentWrites("acme/platform", "infra")[0].State != "running" || f.f.Environments("acme/platform", "infra")[0].Name != "infraweave/infra" {
		t.Fatal("snapshot aliases native records")
	}
}

func TestUnitDeploymentPermissionsAndValidation(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	for _, tc := range []struct {
		method, path, token, body string
		want                      int
	}{
		{http.MethodGet, deploymentProject + "/deployments", "wrong", "", 401},
		{http.MethodGet, "hidden%2Finfra/deployments", f.write, "", 404},
		{http.MethodPost, deploymentProject + "/environments", f.read, `{"name":"prod"}`, 403},
		{http.MethodPost, deploymentProject + "/environments", f.reporter, `{"name":"prod"}`, 403},
		{http.MethodPost, deploymentProject + "/environments", f.write, `{`, 400},
		{http.MethodPost, deploymentProject + "/environments", f.write, `{}`, 400},
		{http.MethodPost, deploymentProject + "/deployments", f.write, `{}`, 400},
		{http.MethodPost, deploymentProject + "/deployments", f.write, `{"environment":"prod","sha":"one","ref":"main","status":"success"}`, 400},
		{http.MethodPost, deploymentProject + "/deployments", f.write, `{"environment":"prod","sha":"one","ref":"main","tag":false,"status":"invented"}`, 400},
		{http.MethodPut, deploymentProject + "/deployments/999", f.write, `{"status":"success"}`, 404},
		{http.MethodPut, deploymentProject + "/deployments/bad", f.write, `{"status":"success"}`, 404},
		{http.MethodPut, deploymentProject + "/deployments/999", f.write, `{"status":"invented"}`, 400},
	} {
		if w := f.send(t, tc.method, tc.path, tc.token, tc.body); w.Code != tc.want {
			t.Fatalf("%s %s=%d want%d: %s", tc.method, tc.path, w.Code, tc.want, w.Body)
		}
	}
	for range 2 {
		w := f.send(t, http.MethodPost, deploymentProject+"/environments", f.write, `{"name":"prod"}`)
		if w.Code != 201 && w.Code != 400 {
			t.Fatal("duplicate environment did not use native validation")
		}
	}
	if len(f.f.Environments("acme/platform", "infra")) != 1 {
		t.Fatal("duplicate environment was created")
	}
	if w := f.send(t, http.MethodPost, deploymentProject+"/deployments", f.write, `{"environment":"prod","sha":"one","ref":"main","tag":false,"status":"running"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := f.f.Deployments("acme/platform", "infra")[0].ID
	if w := f.send(t, http.MethodPut, fmt.Sprintf("acme%%2Fweb/deployments/%d", id), f.write, `{"status":"success"}`); w.Code != 404 {
		t.Fatal("another project updated the deployment")
	}
}

func TestUnitDeploymentLostResponseAndConcurrency(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	f.f.FailNextDeploymentCreate()
	body := `{"environment":"prod","sha":"one","ref":"main","tag":false,"status":"running"}`
	if w := f.send(t, http.MethodPost, deploymentProject+"/deployments", f.write, body); w.Code != 502 {
		t.Fatal("successful create did not lose response")
	}
	if w := f.send(t, http.MethodGet, deploymentProject+"/deployments?environment=prod", f.write, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"sha":"one"`) {
		t.Fatal("lost create is not recoverable")
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			if w := f.send(t, http.MethodPost, deploymentProject+"/deployments", f.write, body); w.Code != 201 {
				t.Error("concurrent deployment create refused", w.Code)
			}
		})
	}
	workers.Wait()
	records := f.f.Deployments("acme/platform", "infra")
	ids := map[int64]bool{}
	for _, record := range records {
		ids[record.ID] = true
	}
	if len(records) != 13 || len(ids) != 13 || len(f.f.Environments("acme/platform", "infra")) != 1 {
		t.Fatal("concurrent deployment/environment identities collided")
	}
}
