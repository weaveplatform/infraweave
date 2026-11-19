package acceptance

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

var policyCaptureIDs = []string{"C9", "C46", "C48", "C49", "C51", "C54", "C56", "C57", "C192", "C291"}

type workflowCapture struct {
	Exchanges []workflowExchange `json:"exchanges"`
}

type workflowExchange struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Request    struct {
		Query string `json:"query"`
	} `json:"request"`
	Response struct {
		Data struct {
			TierFeatures []string        `json:"tierFeatures"`
			PolicyCreate *workflowPolicy `json:"policyCreatev2"`
			PolicyUpdate *workflowPolicy `json:"policyUpdatev2"`
			Stack        *struct {
				Run *workflowRun `json:"run"`
			} `json:"stack"`
		} `json:"data"`
	} `json:"response"`
}

type workflowPolicy struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type workflowRun struct {
	State    string `json:"state"`
	Type     string `json:"type"`
	Receipts []struct {
		PolicyID string `json:"policySlug"`
		Type     string `json:"policyType"`
		Outcome  string `json:"outcome"`
	} `json:"policyReceipts"`
	History []struct {
		State string `json:"state"`
		Note  string `json:"note"`
	} `json:"history"`
	Gate       *workflowSample `json:"gate"`
	Queued     *workflowSample `json:"queued"`
	ResetAfter *workflowSample `json:"resetAfter"`
}

type workflowSample struct {
	Input   string `json:"input"`
	Results string `json:"results"`
	Error   string `json:"error"`
}

// The request bounds distinguish seconds from milliseconds without relying on
// the number of digits in one observed timestamp.
func checkPolicyTimestamps(t *testing.T) {
	t.Helper()
	var capture workflowCapture
	captureJSON(t, "C56", harness.ResponseFile, &capture)
	creations, updates := 0, 0
	var createdAt int64
	for _, exchange := range capture.Exchanges {
		policy := exchange.Response.Data.PolicyCreate
		if policy != nil {
			creations++
			createdAt = policy.CreatedAt
			if createdAt < exchange.StartedAt.Unix() || createdAt > exchange.FinishedAt.Unix() {
				t.Fatalf("createdAt %d outside create request bounds", createdAt)
			}
		} else {
			policy = exchange.Response.Data.PolicyUpdate
			if policy == nil {
				continue
			}
			updates++
		}
		if exchange.StartedAt.IsZero() || exchange.FinishedAt.Before(exchange.StartedAt) ||
			policy.CreatedAt != createdAt || policy.UpdatedAt < exchange.StartedAt.Unix() ||
			policy.UpdatedAt > exchange.FinishedAt.Unix() {
			t.Fatalf("policy timestamps = %+v outside request bounds %s..%s", policy,
				exchange.StartedAt, exchange.FinishedAt)
		}
	}
	if creations != 1 || updates != 1 {
		t.Fatalf("recorded %d creations and %d updates, want one each", creations, updates)
	}
}

func checkPolicyDeleteForce(t *testing.T) {
	t.Helper()
	var capture workflowCapture
	captureJSON(t, "C9", harness.ResponseFile, &capture)
	if len(capture.Exchanges) != 8 || strings.Contains(capture.Exchanges[3].Request.Query, "force") ||
		!strings.Contains(capture.Exchanges[5].Request.Query, "force:true") {
		t.Fatal("deletion capture must compare omitted force with force:true")
	}
}

func checkApprovalRejections(t *testing.T) {
	t.Helper()
	var capture workflowCapture
	captureJSON(t, "C291", harness.ResponseFile, &capture)
	states := map[string]bool{}
	for _, exchange := range capture.Exchanges {
		stack := exchange.Response.Data.Stack
		if stack == nil || stack.Run == nil || stack.Run.State != "FAILED" {
			continue
		}
		run := stack.Run
		if len(run.History) < 2 || len(run.Receipts) < 2 {
			t.Fatal("failed run lacks transition and evaluation evidence")
		}
		before := run.History[1].State
		if run.History[0].State != "FAILED" || run.History[0].Note != "Run rejected by approval policies" ||
			(before != "UNCONFIRMED" && before != "PENDING_REVIEW") {
			t.Fatalf("unexpected rejection history: %+v", run.History)
		}
		// The API returns the latest receipts first. Both latest policies share
		// the same reevaluation, while older approving receipts remain in history.
		outcomes := []string{run.Receipts[0].Outcome, run.Receipts[1].Outcome}
		slices.Sort(outcomes)
		if !slices.Equal(outcomes, []string{"approve", "reject"}) ||
			run.Receipts[0].PolicyID == run.Receipts[1].PolicyID ||
			run.Receipts[0].Type != "APPROVAL" || run.Receipts[1].Type != "APPROVAL" {
			t.Fatalf("latest approval receipts do not show mixed outcomes: %+v", run.Receipts[:2])
		}
		states[before] = true
	}
	if len(states) != 2 {
		t.Fatalf("rejection captured from %v, want both review states", states)
	}
}

func checkFlagResetEvidence(t *testing.T) {
	t.Helper()
	var capture workflowCapture
	captureJSON(t, "C51", harness.ResponseFile, &capture)
	samples := map[string]*workflowSample{}
	for _, exchange := range capture.Exchanges {
		stack := exchange.Response.Data.Stack
		if stack == nil || stack.Run == nil {
			continue
		}
		for name, sample := range map[string]*workflowSample{
			"queued": stack.Run.Queued, "gate": stack.Run.Gate, "after": stack.Run.ResetAfter,
		} {
			if sample != nil {
				samples[name] = sample
			}
		}
	}
	if len(samples) != 3 {
		t.Fatalf("recorded samples %v, want queued, gate and after", samples)
	}
	for name, sample := range samples {
		var input struct {
			Run struct {
				State string   `json:"state"`
				Flags []string `json:"flags"`
			} `json:"run"`
		}
		var decision struct {
			Flag  []string `json:"flag"`
			Reset []string `json:"reset_flag"` //nolint:tagliatelle // Spacelift's decision field is snake_case.
		}
		if err := json.Unmarshal([]byte(sample.Input), &input); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(sample.Results), &decision); err != nil {
			t.Fatal(err)
		}
		if sample.Error != "" {
			t.Fatalf("%s sample failed: %s", name, sample.Error)
		}
		wantFlags, wantSet, wantReset := []string{}, []string{}, []string{"capture-643-reset"}
		wantState := "UNCONFIRMED"
		switch name {
		case "queued":
			wantState, wantSet, wantReset = "QUEUED", []string{"capture-643-reset"}, []string{}
		case "gate":
			wantFlags = []string{"capture-643-reset"}
		}
		if input.Run.State != wantState || !slices.Equal(input.Run.Flags, wantFlags) ||
			!slices.Equal(decision.Flag, wantSet) || !slices.Equal(decision.Reset, wantReset) {
			t.Fatalf("%s sample: input %+v, decision %+v", name, input, decision)
		}
	}
}

func checkFreeTierEvidence(t *testing.T) {
	t.Helper()
	var console struct {
		CapturedAt time.Time `json:"capturedAt"`
		Warning    []string  `json:"warning"`
	}
	captureJSON(t, "C48", "console.json", &console)
	if console.CapturedAt.IsZero() || !slices.Contains(console.Warning,
		"Access, Push, Trigger and Notification policies will not be evaluated for your account. Upgrade your plan to make use of them.") {
		t.Fatal("the live console entitlement warning is missing")
	}
	var capture workflowCapture
	captureJSON(t, "C48", harness.ResponseFile, &capture)
	created, evaluated := map[string]bool{}, map[string]bool{}
	for _, exchange := range capture.Exchanges {
		data := exchange.Response.Data
		if slices.Contains(data.TierFeatures, "WORKFLOW_POLICIES") {
			t.Fatal("capture unexpectedly advertises workflow policy entitlement")
		}
		if data.PolicyCreate != nil {
			created[data.PolicyCreate.Type] = true
		}
		if data.Stack != nil && data.Stack.Run != nil {
			for _, receipt := range data.Stack.Run.Receipts {
				evaluated[receipt.Type] = true
			}
		}
	}
	wantCreated := map[string]bool{
		"PLAN": true, "APPROVAL": true, "GIT_PUSH": true, "TRIGGER": true,
		"INITIALIZATION": true, "TASK": true, "NOTIFICATION": true, "INTENT": true,
	}
	wantEvaluated := map[string]bool{"PLAN": true, "APPROVAL": true, "INITIALIZATION": true}
	if !reflect.DeepEqual(created, wantCreated) || !reflect.DeepEqual(evaluated, wantEvaluated) {
		t.Fatalf("free-tier observations: created %v, evaluated %v", created, evaluated)
	}
}
