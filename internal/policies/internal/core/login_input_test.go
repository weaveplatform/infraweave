package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
)

func loginRequest() accessapi.LoginRequest {
	member, subject := true, "verified-upstream-subject"
	requestIP, creatorIP := "192.0.2.7", "192.0.2.8"
	return accessapi.LoginRequest{
		Account: kernel.NewAccountID(), At: time.Unix(1795140000, 123456789), RemoteIP: &requestIP,
		Session: accessapi.LoginSession{
			Member: &member, Login: "alice", Name: "Alice", IDPSubject: &subject,
			CreatorIP: &creatorIP, TeamsKnown: true, Teams: []string{"upstream-team"},
		},
		Spaces: []accessapi.PolicySpace{{ID: kernel.NewULID(), Slug: "legacy", Name: "Legacy", Labels: []string{}}},
		Roles:  []accessapi.PolicyRole{{ID: kernel.NewULID(), Slug: "space-reader", Name: "Space reader"}},
	}
}

func TestUnitLoginInputDrivesBothRealDialectsWithOriginalIdentityFacts(t *testing.T) {
	engine, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	condition := `input.session.member
  input.session.teams == ["upstream-team"]
  input.session.idp_subject == "verified-upstream-subject"
  input.session.creator_ip == "192.0.2.8"
  input.request.remote_ip == "192.0.2.7"
  input.request.timestamp_ns == 1795140000123456789
  input.roles[0].id == "space-reader"
  input.roles[0].slug == "space-reader"
  input.roles[0].ulid != input.roles[0].id
  input.spaces[0].id == "legacy"
  not input.session.admin
  not input.session.stack_id`
	for _, language := range []struct {
		engine rego.EngineType
		body   string
	}{
		{rego.V0, "package infraweave\nallow {\n" + condition + "\n}\n"},
		{rego.V1, "package infraweave\nallow if {\n" + condition + "\n}\n"},
	} {
		t.Run(string(language.engine), func(t *testing.T) {
			facts := loginRequest()
			for _, member := range []bool{true, false, true} {
				facts.Session.Member = &member
				input, err := buildLoginInput(t.Context(), facts)
				if err != nil {
					t.Fatal(err)
				}
				result, err := engine.Evaluate(t.Context(), rego.Request{
					Policy: rego.Policy{Type: rego.Login, Engine: language.engine, Body: language.body}, Input: input,
				})
				if err != nil {
					t.Fatal(err)
				}
				var decision struct {
					Allow bool `json:"allow"`
				}
				if err := json.Unmarshal(result.Decision, &decision); err != nil || decision.Allow != member {
					t.Fatalf("original membership/cache isolation changed: %s, %v", result.Decision, err)
				}
			}
		})
	}
}

func TestUnitLoginInputPreservesNullEmptyAndUnavailableFacts(t *testing.T) {
	for _, teams := range [][]string{nil, {}} {
		facts := loginRequest()
		facts.Session.Teams = teams
		facts.Session.IDPSubject = nil
		raw, err := buildLoginInput(t.Context(), facts)
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Session map[string]json.RawMessage `json:"session"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		want := "[]"
		if teams == nil {
			want = "null"
		}
		if string(document.Session["teams"]) != want || document.Session["idp_subject"] != nil {
			t.Fatalf("nullable teams or unavailable authenticating subject changed: %s", raw)
		}
	}
	for name, mutate := range map[string]func(*accessapi.LoginRequest){
		"unknown upstream membership": func(f *accessapi.LoginRequest) { f.Session.Member = nil },
		"unknown original teams":      func(f *accessapi.LoginRequest) { f.Session.TeamsKnown = false },
		"missing account":             func(f *accessapi.LoginRequest) { f.Account = kernel.AccountID{} },
		"missing request time":        func(f *accessapi.LoginRequest) { f.At = time.Time{} },
		"invalid role identity":       func(f *accessapi.LoginRequest) { f.Roles[0].ID = kernel.ULID{} },
		"invalid space identity":      func(f *accessapi.LoginRequest) { f.Spaces[0].ID = kernel.ULID{} },
		"invalid person identity":     func(f *accessapi.LoginRequest) { f.Session.Identity = &accessapi.ReviewIdentity{} },
		"invalid federation": func(f *accessapi.LoginRequest) {
			f.Session.Identity = &accessapi.ReviewIdentity{Member: kernel.NewMemberID(), Federations: []accessapi.ReviewFederation{{Subject: "not-an-authenticating-subject"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			facts := loginRequest()
			mutate(&facts)
			if raw, err := buildLoginInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) || raw != nil {
				t.Fatalf("unavailable/invalid facts became usable input: %s, %v", raw, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := buildLoginInput(ctx, loginRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled login input did not stop: %v", err)
	}
}

func TestUnitLoginInputSeparatesLinkedIdentityFromAuthenticatingSubject(t *testing.T) {
	facts := loginRequest()
	provider := kernel.NewULID()
	facts.Session.Identity = &accessapi.ReviewIdentity{
		Member: kernel.NewMemberID(),
		Federations: []accessapi.ReviewFederation{{
			Provider: provider, Subject: "different-linked-identity-subject",
		}},
	}
	raw, err := buildLoginInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Session struct {
			Subject  string `json:"idp_subject"` //nolint:tagliatelle // The published LOGIN input uses this wire name.
			Identity struct {
				ULID        string                                `json:"ulid"`
				Federations map[string]map[string]json.RawMessage `json:"federations"`
			} `json:"identity"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	federation := document.Session.Identity.Federations[provider.String()]
	if document.Session.Subject != *facts.Session.IDPSubject || document.Session.Identity.ULID != facts.Session.Identity.Member.String() ||
		string(federation["id"]) != `"different-linked-identity-subject"` || federation["username"] != nil {
		t.Fatalf("linked identity substituted authentication facts: %s", raw)
	}
	facts.Session.Machine = true
	raw, err = buildLoginInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	var machine struct {
		Session struct {
			Admin json.RawMessage `json:"admin"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &machine); err != nil || string(machine.Session.Admin) != `""` {
		t.Fatalf("machine admin marker changed: %s, %v", raw, err)
	}
}
