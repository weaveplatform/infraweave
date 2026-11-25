package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/cloud/entrafake"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// The operator's Entra tenant and registered application, and the
// application's client secret, as the scenario's servers know them.
const (
	operatorTenant = "0a0b0c0d-1111-2222-3333-444455556666"
	operatorClient = "7a7b7c7d-8888-9999-aaaa-bbbbccccdddd"
	operatorSecret = "acceptance-entra-client-secret" //nolint:gosec // a test credential
)

// Graph's resource and its delegated User.Read, which an Azure
// integration's application asks.
const (
	graphResource = "00000003-0000-0000-c000-000000000000"
	userRead      = "e1fe6dd8-ba31-4d61-89e7-88639da4683d"
)

// entraSteps serve a fake Entra ID and Microsoft Graph for the scenario's
// servers.
type entraSteps struct {
	w    *harness.World
	fake *entrafake.Fake
	srv  *entrafake.Server
}

func (e *entraSteps) register(sc *godog.ScenarioContext) {
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if e.srv != nil {
			e.srv.Close()
		}
		return ctx, err
	})
	sc.Step(`^the installation registers an Entra application for Azure integrations$`, e.registered)
	sc.Step(`^Microsoft Graph throttles the next (\d+) requests$`, e.throttle)
	sc.Step(`^Microsoft Graph is unavailable$`, e.unavailable)
	sc.Step(`^Microsoft Graph is available again$`, e.available)
	sc.Step(`^Microsoft Graph holds (\d+) Entra applications?$`, e.holds)
	sc.Step(`^Microsoft Graph holds the Entra application of the Azure integration "(\w+)" of the account "(\w+)"$`,
		e.application)
	sc.Step(`^Microsoft Graph was throttled and answered on a retry$`, e.retried)
	sc.Step(`^the answer's adminConsentURL asks the tenant "([^"]+)" to consent to its application$`, e.consentURL)
}

// registered serves the fake with the operator's application registered
// in its tenant, and points the scenario's servers at it: cloud.azure
// names the application, the fake's URL as both the authority and Graph,
// and its certificate to trust, and the environment carries the client
// secret azidentity's environment credential reads. AZURE_TOKEN_CREDENTIALS
// keeps the default chain to that credential, so it probes no managed
// identity endpoint.
func (e *entraSteps) registered() error {
	e.fake = entrafake.New()
	e.fake.AddSecret(operatorTenant, operatorClient, operatorSecret)
	e.srv = e.fake.Start()
	ca := filepath.Join(e.w.Dir, "entra-ca.pem")
	if err := os.WriteFile(ca, e.srv.CA, 0o600); err != nil {
		return fmt.Errorf("%w: write Entra's certificate: %w", harness.ErrStep, err)
	}
	cloudSettings(e.w)["azure"] = map[string]any{
		"tenant_id": operatorTenant, "client_id": operatorClient,
		"authority_host": e.srv.URL + "/", "graph_endpoint": e.srv.URL, "ca_file": ca,
	}
	for k, v := range map[string]string{
		"AZURE_TOKEN_CREDENTIALS": "EnvironmentCredential", "AZURE_TENANT_ID": operatorTenant,
		"AZURE_CLIENT_ID": operatorClient, "AZURE_CLIENT_SECRET": operatorSecret,
		"AZURE_FEDERATED_TOKEN_FILE": "", "AZURE_CLIENT_CERTIFICATE_PATH": "",
		"AZURE_USERNAME": "", "AZURE_PASSWORD": "",
	} {
		e.w.Setenv(k, v)
	}
	return nil
}

// cloudSettings is the scenario's cloud settings section, created empty
// when no step has set one.
func cloudSettings(w *harness.World) map[string]any {
	if w.Settings["cloud"] == nil {
		w.Settings["cloud"] = map[string]any{}
	}
	return w.Settings["cloud"]
}

func (e *entraSteps) need() error {
	if e.fake == nil {
		return fmt.Errorf("%w: no Entra application in this scenario", harness.ErrStep)
	}
	return nil
}

// throttle answers the next n Graph requests 429 with Retry-After: 1, which
// the client waits out within the request's deadline.
func (e *entraSteps) throttle(n int) error {
	if err := e.need(); err != nil {
		return err
	}
	e.fake.Reset()
	e.fake.RetryAfter(1)
	e.fake.FailNext(entrafake.FaultThrottle, n)
	return nil
}

// unavailable answers every Graph request 503 with a Retry-After past any
// request's deadline, so the client gives up at once.
func (e *entraSteps) unavailable() error {
	if err := e.need(); err != nil {
		return err
	}
	e.fake.RetryAfter(3600)
	e.fake.FailNext(entrafake.FaultUnavailable, 1<<30)
	return nil
}

// available answers Graph's requests again.
func (e *entraSteps) available() error {
	if err := e.need(); err != nil {
		return err
	}
	e.fake.Reset()
	return nil
}

// holds checks how many applications exist in the operator's tenant.
func (e *entraSteps) holds(n int) error {
	if err := e.need(); err != nil {
		return err
	}
	if got := len(e.fake.Applications()); got != n {
		return fmt.Errorf("%w: Graph holds %d applications, want %d", harness.ErrStep, got, n)
	}
	return nil
}

// application checks the application of the integration the scenario
// names name: created by the operator's application, multi-tenant, named
// infraweave-<account>-<integration ULID> and asking Graph's User.Read.
func (e *entraSteps) application(name, account string) error {
	if err := e.need(); err != nil {
		return err
	}
	want := "infraweave-" + account + "-" + e.w.Resolve("{"+name+"}")
	for _, a := range e.fake.Applications() {
		if a.DisplayName != want {
			continue
		}
		asks := slices.ContainsFunc(a.RequiredResourceAccess, func(r entrafake.RequiredResourceAccess) bool {
			return r.ResourceAppID == graphResource && slices.Contains(r.ResourceAccess,
				entrafake.ResourceAccess{ID: userRead, Type: "Scope"})
		})
		if a.SignInAudience != "AzureADMultipleOrgs" || a.Owner != operatorClient || !asks {
			return fmt.Errorf("%w: application %+v", harness.ErrStep, a)
		}
		return nil
	}
	names := make([]string, 0)
	for _, a := range e.fake.Applications() {
		names = append(names, a.DisplayName)
	}
	return fmt.Errorf("%w: Graph holds %s, not %s", harness.ErrStep, strings.Join(names, ", "), want)
}

// retried checks that Graph throttled a request since the last throttle
// step, and answered the same request when the client retried it.
func (e *entraSteps) retried() error {
	if err := e.need(); err != nil {
		return err
	}
	var throttled, answered bool
	for _, c := range e.fake.Calls() {
		switch {
		case c.Status == 429:
			throttled = true
		case throttled && strings.HasPrefix(c.Path, "/v1.0/") && c.Status/100 == 2:
			answered = true
		}
	}
	if !throttled || !answered {
		return fmt.Errorf("%w: calls %+v", harness.ErrStep, e.fake.Calls())
	}
	return nil
}

// consentURL checks that the last answer, an Azure integration, asks the
// admin of tenant to consent to its own application.
func (e *entraSteps) consentURL(tenant string) error {
	var v struct {
		URL           string `json:"adminConsentURL"` //nolint:tagliatelle // the API names it so
		ApplicationID string `json:"applicationId"`
	}
	r := e.w.Last()
	if r.Status/100 != 2 || json.Unmarshal(r.Body, &v) != nil || v.ApplicationID == "" {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	want := "https://login.microsoftonline.com/" + tenant + "/adminconsent?client_id=" + v.ApplicationID
	if v.URL != want {
		return fmt.Errorf("%w: adminConsentURL %q, want %q", harness.ErrStep, v.URL, want)
	}
	return nil
}
