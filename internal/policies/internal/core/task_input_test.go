package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func taskInputFacts() policyapi.TaskInputFacts {
	base := inputFacts()
	return policyapi.TaskInputFacts{
		Account: base.Account, Stack: base.Stack, At: base.At, Command: "terraform state list", RemoteIP: known("192.0.2.18"),
		Session: policyapi.SessionFacts{
			Machine: false, Admin: known(policyapi.SessionNotAdmin), Login: known("alice"),
			Name: known("Alice"), Teams: known([]string{}),
		},
	}
}

func TestUnitTaskInputPreservesPreCreationFacts(t *testing.T) {
	facts := taskInputFacts()
	raw, err := (&Service{}).BuildTaskInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var input inputs.TaskInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	request, _ := input.Request.Get()
	command, _ := request.Command.Get()
	ns, _ := request.TimestampNS.Get()
	address, _ := request.RemoteIP.Get()
	stack, _ := input.Stack.Get()
	id, _ := stack.ID.Get()
	ss, _ := input.Session.Get()
	login, _ := ss.Login.Get()
	teams, _ := ss.Teams.Get()
	if command != facts.Command || ns != facts.At.UnixNano() || address != "192.0.2.18" || id != facts.Stack.Slug ||
		login != "alice" || teams == nil || len(teams) != 0 || input.Module.Present() {
		t.Fatalf("pre-creation facts changed: %s", raw)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil || len(document) != 3 || document["run"] != nil {
		t.Fatalf("TASK input invented a run or extra owner: %s, %v", raw, err)
	}
}

func TestUnitTaskInputRefusesMissingAndInconsistentFacts(t *testing.T) {
	for name, alter := range map[string]func(*policyapi.TaskInputFacts){
		"account":                func(f *policyapi.TaskInputFacts) { f.Account = kernel.AccountID{} },
		"clock":                  func(f *policyapi.TaskInputFacts) { f.At = time.Time{} },
		"stack":                  func(f *policyapi.TaskInputFacts) { f.Stack.ID = kernel.ULID{} },
		"command":                func(f *policyapi.TaskInputFacts) { f.Command = "" },
		"unknown value":          func(f *policyapi.TaskInputFacts) { f.RemoteIP.Known = false },
		"machine admin mismatch": func(f *policyapi.TaskInputFacts) { f.Session.Admin = known(policyapi.SessionMachineAdmin) },
	} {
		t.Run(name, func(t *testing.T) {
			facts := taskInputFacts()
			alter(&facts)
			if _, err := (&Service{}).BuildTaskInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) {
				t.Fatalf("invalid task facts were not refused: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Service{}).BuildTaskInput(ctx, taskInputFacts()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became an invented policy input: %v", err)
	}
}
