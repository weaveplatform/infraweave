package entrafake_test

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/cloud/entrafake"
)

const tenant = "11111111-2222-3333-4444-555555555555"

// fixture is the fake served over TLS with a client that trusts it.
type fixture struct {
	fake *entrafake.Fake
	srv  *entrafake.Server
	http *http.Client
}

func start(t *testing.T) *fixture {
	t.Helper()
	f := entrafake.New()
	srv := f.Start()
	t.Cleanup(srv.Close)
	return &fixture{fake: f, srv: srv, http: &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: srv.Pool(), MinVersion: tls.VersionTLS12},
	}}}
}

func (f *fixture) do(t *testing.T, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, "/token") {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // a test's read
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out) // a 204 has no body
	return resp.StatusCode, out
}

func (f *fixture) token(t *testing.T, form url.Values) (int, map[string]any) {
	t.Helper()
	return f.do(t, http.MethodPost, "/"+tenant+"/oauth2/v2.0/token", "", form.Encode())
}

func TestUnitTokens(t *testing.T) {
	f := start(t)
	f.fake.AddSecret(tenant, "app", "secret")
	f.fake.AddAssertion(tenant, "pod", "jwt")
	grant := func(kv ...string) url.Values {
		v := url.Values{"grant_type": {"client_credentials"}, "scope": {"https://graph/.default"}}
		for n := 0; n < len(kv); n += 2 {
			v.Set(kv[n], kv[n+1])
		}
		return v
	}
	assertion := "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	for _, c := range []struct {
		name   string
		form   url.Values
		status int
		error  string
	}{
		{"secret", grant("client_id", "app", "client_secret", "secret"), http.StatusOK, ""},
		{"assertion", grant("client_id", "pod", "client_assertion", "jwt", "client_assertion_type", assertion), http.StatusOK, ""},
		{"wrong secret", grant("client_id", "app", "client_secret", "nope"), http.StatusUnauthorized, "invalid_client"},
		{"secret of an assertion client", grant("client_id", "pod", "client_secret", "jwt"), http.StatusUnauthorized, "invalid_client"},
		{"wrong assertion", grant("client_id", "pod", "client_assertion", "other", "client_assertion_type", assertion), http.StatusUnauthorized, "invalid_client"},
		{"assertion type", grant("client_id", "pod", "client_assertion", "jwt"), http.StatusUnauthorized, "invalid_client"},
		{"unknown client", grant("client_id", "ghost", "client_secret", "secret"), http.StatusBadRequest, "unauthorized_client"},
		{"grant type", url.Values{"grant_type": {"password"}}, http.StatusBadRequest, "unsupported_grant_type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, body := f.token(t, c.form)
			if status != c.status || (c.error != "" && body["error"] != c.error) ||
				(c.error == "" && body["access_token"] == "") {
				t.Errorf("%d %v", status, body)
			}
		})
	}
	grants := map[string]int{}
	for _, c := range f.fake.Calls() {
		if c.Status == http.StatusOK {
			grants[c.Grant]++
		}
	}
	if grants[entrafake.GrantSecret] != 1 || grants[entrafake.GrantAssertion] != 1 {
		t.Errorf("grants %v", grants)
	}
	status, body := f.do(t, http.MethodGet, "/"+tenant+"/v2.0/.well-known/openid-configuration", "", "")
	if status != http.StatusOK || body["token_endpoint"] != f.srv.URL+"/"+tenant+"/oauth2/v2.0/token" {
		t.Errorf("discovery %d %v", status, body)
	}
}

func TestUnitApplications(t *testing.T) {
	f := start(t)
	f.fake.AddSecret(tenant, "app", "secret")
	_, tok := f.token(t, url.Values{
		"grant_type": {"client_credentials"}, "client_id": {"app"}, "client_secret": {"secret"},
	})
	token, _ := tok["access_token"].(string)
	if status, _ := f.do(t, http.MethodPost, "/v1.0/applications", "", `{"displayName":"x"}`); status != http.StatusUnauthorized {
		t.Errorf("no token: %d", status)
	}
	if status, _ := f.do(t, http.MethodPost, "/v1.0/applications", "forged", `{"displayName":"x"}`); status != http.StatusUnauthorized {
		t.Errorf("an unknown token: %d", status)
	}
	for _, body := range []string{`{`, `{"displayName":" "}`, `{"displayName":"x","signInAudience":"Everyone"}`} {
		if status, _ := f.do(t, http.MethodPost, "/v1.0/applications", token, body); status != http.StatusBadRequest {
			t.Errorf("%s: %d", body, status)
		}
	}
	status, app := f.do(t, http.MethodPost, "/v1.0/applications", token, `{"displayName":"single"}`)
	if status != http.StatusCreated || app["signInAudience"] != "AzureADMyOrg" {
		t.Fatalf("create %d %v", status, app)
	}
	id, _ := app["id"].(string)
	f.fake.DelayReplication(1)
	_, late := f.do(t, http.MethodPost, "/v1.0/applications", token, `{"displayName":"late"}`)
	lateID, _ := late["id"].(string)
	if status, _ := f.do(t, http.MethodGet, "/v1.0/applications/"+lateID, token, ""); status != http.StatusNotFound {
		t.Errorf("an unreplicated read: %d", status)
	}
	if status, got := f.do(t, http.MethodGet, "/v1.0/applications/"+lateID, token, ""); status != http.StatusOK || got["displayName"] != "late" {
		t.Errorf("a replicated read: %d %v", status, got)
	}
	f.fake.FailNext(entrafake.FaultServerError, 1)
	if status, _ := f.do(t, http.MethodGet, "/v1.0/applications/"+id, token, ""); status != http.StatusInternalServerError {
		t.Errorf("a server error: %d", status)
	}
	if status, _ := f.do(t, http.MethodDelete, "/v1.0/applications/"+id, token, ""); status != http.StatusNoContent {
		t.Errorf("delete: %d", status)
	}
	if status, _ := f.do(t, http.MethodDelete, "/v1.0/applications/"+id, token, ""); status != http.StatusNotFound {
		t.Errorf("a second delete: %d", status)
	}
	if apps := f.fake.Applications(); len(apps) != 1 || apps[0].DisplayName != "late" || apps[0].Owner != "app" {
		t.Errorf("applications %+v", apps)
	}
	f.fake.FailNext(entrafake.FaultThrottle, 5)
	f.fake.DelayReplication(3)
	f.fake.Reset()
	if len(f.fake.Calls()) != 0 {
		t.Error("Reset kept the calls")
	}
	if status, _ := f.do(t, http.MethodGet, "/v1.0/applications/"+lateID, token, ""); status != http.StatusOK {
		t.Errorf("Reset kept the faults: %d", status)
	}
}
