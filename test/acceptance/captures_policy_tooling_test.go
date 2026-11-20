package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var policyToolingCaptures = []string{"C47", "C50", "C52", "C53", "C273"}

type toolingExchange struct {
	Stage   string `json:"stage"`
	Source  string `json:"source"`
	Request struct {
		Operation string          `json:"operationName"`
		Variables json.RawMessage `json:"variables"`
	} `json:"request"`
	Response json.RawMessage `json:"response"`
}

func TestUnitPolicyToolingCaptureEvidence(t *testing.T) {
	for _, id := range policyToolingCaptures {
		t.Run(id, func(t *testing.T) { checkPolicyToolingCapture(t, id) })
	}
}

func checkPolicyToolingCapture(t *testing.T, id string) {
	t.Helper()
	var document struct {
		Exchanges []toolingExchange `json:"exchanges"`
	}
	captureJSON(t, id, "response.json", &document)
	exchanges := document.Exchanges
	if len(exchanges) == 0 {
		t.Fatal("no recorded exchanges")
	}
	checkToolingCleanup(t, exchanges[len(exchanges)-1])
	checkToolingCredentials(t, exchanges)
	switch id {
	case "C47":
		var create struct {
			Input struct{ Type string } `json:"input"`
		}
		decodeToolingJSON(t, exchanges[1].Request.Variables, &create)
		if create.Input.Type != "ACCESS" {
			t.Fatalf("legacy creation control = %q", create.Input.Type)
		}
	case "C50":
		before := toolingUpdateBody(t, toolingStage(t, exchanges, "C50-configure-login"))
		after := toolingUpdateBody(t, toolingStage(t, exchanges, "C50-login-flag-false-active-update"))
		if strings.Replace(before, `flag contains "capture-644-login-flag" if { true }`, `flag contains "capture-644-login-flag" if { false }`, 1) != after || before == after {
			t.Fatal("LOGIN comparison must change only the flag condition")
		}
		deny := toolingUpdateBody(t, toolingStage(t, exchanges, "C50-login-deny-key-control"))
		if !strings.Contains(deny, "deny if { input.session.login == ") {
			t.Fatal("no actual key-specific boolean deny control")
		}
	case "C52":
		checkToolingInitialization(t, toolingStage(t, exchanges, "C52-initialization-result"))
	case "C53":
		checkToolingSampling(t, exchanges)
	case "C273":
		checkToolingPage(t, exchanges)
	default:
		t.Fatalf("no tooling evidence check for %s", id)
	}
}

func decodeToolingJSON(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}

func toolingStage(t *testing.T, exchanges []toolingExchange, stage string) toolingExchange {
	t.Helper()
	for _, exchange := range exchanges {
		if exchange.Stage == stage {
			return exchange
		}
	}
	t.Fatalf("missing exchange %s", stage)
	return toolingExchange{}
}

func toolingUpdateBody(t *testing.T, exchange toolingExchange) string {
	t.Helper()
	var variables struct {
		Input struct{ Body string } `json:"input"`
	}
	decodeToolingJSON(t, exchange.Request.Variables, &variables)
	return variables.Input.Body
}

func checkToolingCleanup(t *testing.T, exchange toolingExchange) {
	t.Helper()
	var response struct {
		Data struct {
			Scheme   string                          `json:"authorizationScheme"`
			Viewer   struct{ Admin bool }            `json:"viewer"`
			Keys     []json.RawMessage               `json:"apiKeys"`
			Stacks   []json.RawMessage               `json:"stacks"`
			Policy   []json.RawMessage               `json:"policies"`
			Users    []struct{ Status, Role string } `json:"managedUsers"`
			Sessions []struct{ Current, Admin bool } `json:"sessions"`
		} `json:"data"`
	}
	decodeToolingJSON(t, exchange.Response, &response)
	d := response.Data
	if exchange.Stage != "cleanup-final-inventory" || d.Scheme != "MANAGED_USERS" || !d.Viewer.Admin || d.Keys == nil || len(d.Keys)+len(d.Stacks)+len(d.Policy) != 0 {
		t.Fatalf("scheme, owner or resource cleanup not verified: %+v", d)
	}
	if len(d.Users) != 1 || d.Users[0].Status != "ACTIVE" || d.Users[0].Role != "OWNER" || len(d.Sessions) != 1 || !d.Sessions[0].Current || !d.Sessions[0].Admin {
		t.Fatal("original owner/session inventory was not restored")
	}
}

func checkToolingCredentials(t *testing.T, exchanges []toolingExchange) {
	t.Helper()
	for _, exchange := range exchanges {
		if len(exchange.Request.Variables) > 0 {
			var variables struct{ Secret string }
			decodeToolingJSON(t, exchange.Request.Variables, &variables)
			if variables.Secret != "" && variables.Secret != "<redacted>" {
				t.Fatal("unredacted test key in recorded admission variables")
			}
		}
		if len(exchange.Response) == 0 {
			continue
		}
		var response struct {
			Data struct {
				Create *struct{ Secret string } `json:"apiKeyCreate"`
				User   *struct{ JWT string }    `json:"apiKeyUser"`
			} `json:"data"`
		}
		decodeToolingJSON(t, exchange.Response, &response)
		if key := response.Data.Create; key != nil && key.Secret != "<redacted>" {
			t.Fatal("unredacted created key secret")
		}
		if user := response.Data.User; user != nil && user.JWT != "<redacted>" {
			t.Fatal("unredacted issued JWT")
		}
	}
}

func checkToolingInitialization(t *testing.T, exchange toolingExchange) {
	t.Helper()
	var response struct {
		Data struct {
			Stack struct {
				Run struct {
					History  []struct{ State, Note string } `json:"history"`
					Receipts []struct {
						Type    string `json:"policyType"`
						Outcome string `json:"outcome"`
						Sampled bool   `json:"sampled"`
					} `json:"policyReceipts"`
				} `json:"run"`
			} `json:"stack"`
		} `json:"data"`
	}
	decodeToolingJSON(t, exchange.Response, &response)
	run := response.Data.Stack.Run
	states := make([]string, 0, len(run.History))
	for _, item := range run.History {
		states = append(states, item.State)
	}
	if !slices.Equal(states, []string{"FAILED", "READY", "QUEUED"}) || run.History[0].Note != "job assignment failed: denied by 1 initialization policy" {
		t.Fatalf("server initialization history = %+v", run.History)
	}
	if len(run.Receipts) != 1 || run.Receipts[0].Type != "INITIALIZATION" || run.Receipts[0].Outcome != "deny" || !run.Receipts[0].Sampled {
		t.Fatalf("initialization receipt = %+v", run.Receipts)
	}
}

func checkToolingSampling(t *testing.T, exchanges []toolingExchange) {
	t.Helper()
	type capture struct {
		ID         string `json:"id"`
		CapturedAt int64  `json:"capturedAt"`
	}
	var before, after struct {
		Data struct {
			Plan []capture `json:"plan"`
		} `json:"data"`
	}
	decodeToolingJSON(t, toolingStage(t, exchanges, "C53-sample-false-result").Response, &before)
	decodeToolingJSON(t, toolingStage(t, exchanges, "C53-sample-true-result").Response, &after)
	if len(before.Data.Plan) == 0 || len(after.Data.Plan) == 0 || before.Data.Plan[0] != after.Data.Plan[0] {
		t.Fatal("bounded sample comparison did not retain the same latest input capture")
	}
	var sensitive struct {
		Data struct {
			Stack struct {
				Run struct {
					Sample struct{ Input string } `json:"policyReceiptSample"`
				} `json:"run"`
			} `json:"stack"`
		} `json:"data"`
	}
	decodeToolingJSON(t, toolingStage(t, exchanges, "C53-sensitive-documents").Response, &sensitive)
	input := sensitive.Data.Stack.Run.Sample.Input
	const dummy = "capture-644-dummy-secret"
	if strings.Contains(input, dummy) {
		t.Fatal("real sampled PLAN input contains the sensitive plaintext")
	}
	var document struct {
		Terraform struct {
			Changes []struct {
				Address string `json:"address"`
				Change  struct {
					After     struct{ Input string } `json:"after"`
					Sensitive struct{ Input bool }   `json:"after_sensitive"` //nolint:tagliatelle // Terraform's wire field.
				} `json:"change"`
			} `json:"resource_changes"` //nolint:tagliatelle // Terraform's wire field.
		} `json:"terraform"`
		Spacelift struct {
			Commit struct{ Hash string } `json:"commit"`
		} `json:"spacelift"`
	}
	decodeToolingJSON(t, []byte(input), &document)
	digest := sha256.Sum256([]byte(dummy))
	want := hex.EncodeToString(digest[len(digest)-4:])
	if document.Spacelift.Commit.Hash != "6699d52b96b83cb8785243d406f085e0515ca423" || len(document.Terraform.Changes) != 1 ||
		document.Terraform.Changes[0].Address != "terraform_data.sensitive" ||
		!document.Terraform.Changes[0].Change.Sensitive.Input ||
		document.Terraform.Changes[0].Change.After.Input != want {
		t.Fatalf("sensitive sample lacks the pinned fixture, marker or hash %s", want)
	}
}

func checkToolingPage(t *testing.T, exchanges []toolingExchange) {
	t.Helper()
	operations := map[string]bool{}
	counts := map[string]int{}
	for _, exchange := range exchanges {
		if exchange.Source != "browser-network" {
			continue
		}
		operations[exchange.Request.Operation] = true
		if exchange.Request.Operation == "GetPolicyForDetails" {
			var response struct {
				Data struct {
					Policy struct {
						Type        string            `json:"type"`
						Attachments []json.RawMessage `json:"attachedStacks"`
					} `json:"policy"`
				} `json:"data"`
			}
			decodeToolingJSON(t, exchange.Response, &response)
			counts[response.Data.Policy.Type] = len(response.Data.Policy.Attachments)
		}
	}
	for _, op := range []string{"GetPolicyForDetails", "GetPolicyEvaluationRecords", "GetPolicyEvaluationSample", "SearchEvaluationRecords", "SearchEvaluationRecordSuggestions", "SimulatePolicyV2"} {
		if !operations[op] {
			t.Errorf("no actual browser request for %s", op)
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"PLAN": 1, "NOTIFICATION": 0}) {
		t.Fatalf("browser detail attachment counts = %v", counts)
	}
	notification, err := os.ReadFile(filepath.Join(capturesRoot, "C273", "ui-notification.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(notification), `link "Used by`) || !strings.Contains(string(notification), `link "Samples 0"`) {
		t.Fatal("NOTIFICATION snapshot must show Samples and omit Used by")
	}
}
