package acceptance

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerP13bCaptureReplays(
	parent *testing.T,
	full map[string]func(*testing.T, *sdk.Client),
	subsets map[string]func(*testing.T, *sdk.Client) []string,
	workers **harness.FeatureReplay,
) {
	full["policy-catalogue"] = func(t *testing.T, _ *sdk.Client) {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		name := "C54 pages every captured served catalogue member through the public API"
		contents := []byte(`@postgres
Feature: Captured policy catalogue
  Scenario: ` + name + `
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    Then token "h" pages all captured policy templates through B
`)
		(*workers).Run(t, "C54", contents, []string{name})
	}
	subsets["policy-access-retirement"] = replayAccessRetirement
	registerInitializationToolingReplay(parent, subsets, workers)
	registerPageToolingReplay(parent, subsets, workers)
	registerSamplingToolingReplay(parent, subsets, workers)
	subsets["policy-login-session-contract"] = func(t *testing.T, _ *sdk.Client) []string {
		if *workers == nil {
			*workers = harness.NewFeatureReplay(parent, append(slices.Clone(areas), registerCaptureWorkflow))
		}
		contents, names := loginToolingReplayFeature(t)
		(*workers).Run(t, "C50", contents, names)
		return []string{
			"/exchanges/7/response/data/accountUpdateAuthorizationScheme",
			"/exchanges/9/response/errors/0/message",
			"/exchanges/15/response/data/viewer",
			"/exchanges/21/response/data/accountUpdateAuthorizationScheme",
			"/exchanges/24/response/data/apiKeyUser",
			"/exchanges/27/response/data/apiKeys",
		}
	}
}

func replayAccessRetirement(t *testing.T, client *sdk.Client) []string {
	t.Helper()
	r := c48Replay{t: t, client: client}
	before := map[string][]string{"/policies": r.inventory("/api/v1/policies"), "/stacks": r.inventory("/api/v1/stacks")}
	var capture struct {
		Exchanges []struct {
			Request struct {
				Variables struct {
					Input map[string]any `json:"input"`
				} `json:"variables"`
			} `json:"request"`
		} `json:"exchanges"`
	}
	captureJSON(t, "C47", harness.ResponseFile, &capture)
	if len(capture.Exchanges) < 2 || capture.Exchanges[1].Request.Variables.Input["type"] != "ACCESS" {
		t.Fatal("C47 lacks its actual legacy creation control")
	}
	var failure sdk.Problem
	input := maps.Clone(capture.Exchanges[1].Request.Variables.Input)
	input["space"] = "acme"
	input["body"] = "package infraweave\n"
	response, err := client.Do(t.Context(), "POST", "/api/v1/policies", input)
	// ACCESS retirement is an explicit ADR 0009/0021 deviation. The recorded
	// upstream legacy-space error and unavailable legacy comparison stay facts.
	if response == nil || response.Status != 400 {
		t.Fatalf("C47 retired ACCESS creation: %+v/%v", response, err)
	}
	if json.Unmarshal(response.Body, &failure) != nil || failure.Status != 400 || failure.Slug() != "invalid-request" ||
		!strings.Contains(failure.Detail, `Error at "/type"`) {
		t.Fatalf("C47 refused a field other than the retired type: %s", response.Body)
	}
	for path, ids := range before {
		if got := r.inventory("/api/v1" + path); !reflect.DeepEqual(got, ids) {
			t.Fatalf("C47 changed %s inventory: %v vs %v", path, got, ids)
		}
	}
	return []string{"/exchanges/3/response/data/stacks", "/exchanges/3/response/data/policies"}
}

func loginToolingReplayFeature(t *testing.T) ([]byte, []string) {
	t.Helper()
	var capture struct {
		Exchanges []toolingExchange `json:"exchanges"`
	}
	captureJSON(t, "C50", harness.ResponseFile, &capture)
	bodies := make([]string, 3)
	for i, stage := range []string{"C50-configure-login", "C50-login-flag-false-active-update", "C50-login-deny-key-control"} {
		body := toolingUpdateBody(t, toolingStage(t, capture.Exchanges, stage))
		body = strings.ReplaceAll(body, `\n`, "\n")
		body = strings.Replace(body, "package spacelift", "package infraweave", 1)
		// Captured virtual-user identity and root alias map to this real
		// secret key's documented native LOGIN facts; raw captures stay intact.
		body = strings.ReplaceAll(body, "api::01K00000000000000000000010", harness.Member.Key)
		body = strings.ReplaceAll(body, `roles["root"]`, `roles["acme"]`)
		input := map[string]string{"name": "C50 recorded LOGIN control", "body": body, "engineType": "REGO_V1"}
		if i == 0 {
			input["type"] = "LOGIN"
		}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		bodies[i] = string(raw)
	}
	name := "C50 replays scheme and session effects while testing the approved LOGIN contract"
	contents := fmt.Sprintf(`@postgres @objectstore
Feature: Recorded LOGIN tooling contract
  Scenario: %s
    Given two infraweave servers, A and B, on one database
    And the home account lets API keys manage API keys
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "before"
    When token "h" sends "POST /api/v1/policies" with '%s' through A
    Then it answers 201
    And the answer's id names "guard"
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    Then it answers 200
    And the viewer through A with token "before" answers 401 within a second
    And the member key is exchanged through A for token "allowed"
    And token "allowed"'s read of the space "acme" through B allows exactly the role "space-writer"'s actions
    When token "h" sends "PUT /api/v1/policies/{guard}" with '%s' through A
    Then it answers 200
    And the viewer through B with token "allowed" answers 401 within a second
    And the member key is exchanged through B for token "fresh"
    And token "fresh"'s read of the space "acme" through A allows exactly the role "space-writer"'s actions
    When token "h" sends "PUT /api/v1/policies/{guard}" with '%s' through B
    Then it answers 200
    And the LOGIN caller's permission rows are recorded as "before-deny"
    And exchanging the member key through A answers a 403 problem "forbidden"
    And the LOGIN caller's permission rows still match "before-deny"
    And policy "guard" has 3 committed LOGIN evaluations without run effects
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/api-keys/01KACME000000000000000000A" through B
    Then it answers 200
    And exchanging the member key through A answers a 401 problem "unauthenticated"
    And token "h" sends "GET /api/v1/api-keys/01KACME000000000000000000A" through A
    And it answers a 404 problem "not-found"
    And token "h" sends "DELETE /api/v1/policies/{guard}" through B
    And it answers 200
    And the viewer through A with token "h" answers 200
`, name, bodies[0], bodies[1], bodies[2])
	return []byte(contents), []string{name}
}
