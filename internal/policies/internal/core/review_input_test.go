package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func TestUnitApprovalReviewFactsDriveBothRegoDialects(t *testing.T) {
	facts := inputFacts()
	facts.Type = policyapi.Approval
	facts.Run.State = kernel.RunStateUnconfirmed
	role := policyapi.RoleFacts{ID: "space-writer", Name: "Space writer", Slug: "space-writer", ULID: kernel.NewULID().String()}
	identity := policyapi.IdentityFacts{ULID: known(kernel.NewULID().String()), Federations: known(map[string]policyapi.FederationFacts{
		"oidc": {ID: known("subject-a"), Username: known("alice")},
	})}
	review := policyapi.ReviewFacts{
		Author:       "alice",
		State:        kernel.RunStateUnconfirmed,
		StateVersion: 5,
		AuthorRoles:  known([]policyapi.RoleFacts{role}),
		Identity:     known(identity),
		Note:         known("reviewed"),
		Request:      known(policyapi.RequestFacts{At: facts.At, RemoteIP: known("198.51.100.4")}),
		Session: known(
			policyapi.SessionFacts{
				Machine:  false,
				Admin:    known(policyapi.SessionIsAdmin),
				Login:    known("alice"),
				Name:     known("Alice"),
				Teams:    known([]string{"platform"}),
				Identity: known(identity),
			},
		),
	}
	older := review
	older.Author = "bob"
	older.State = kernel.RunStateQueued
	older.StateVersion = 1
	facts.Reviews = known(policyapi.ReviewsFacts{
		Current: policyapi.ReviewGroupFacts{Approvals: []policyapi.ReviewFacts{review}},
		Older:   policyapi.ReviewGroupFacts{Rejections: []policyapi.ReviewFacts{older}},
	})
	raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rego.New(rego.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
  count(input.reviews.current.approvals) == 1
  count(input.reviews.current.rejections) == 0
  r := input.reviews.current.approvals[0]
  r.author == "alice"
  r.state == "UNCONFIRMED"
  r.state_version == 5
  r.author_roles[0].id == "space-writer"
  r.author_roles[0].name == "Space writer"
  r.author_roles[0].slug == "space-writer"
  r.author_roles[0].ulid == %q
  r.identity.ulid == %q
  r.identity.federations.oidc.id == "subject-a"
  r.identity.federations.oidc.username == "alice"
  r.note == "reviewed"
  r.request.timestamp_ns == %d
  r.request.remote_ip == "198.51.100.4"
  r.session.machine == false
  r.session.admin == true
  r.session.teams == ["platform"]
  r.session.name == "Alice"
  r.session.login == "alice"
  r.session.identity.ulid == r.identity.ulid
  count(input.reviews.older.approvals) == 0
  count(input.reviews.older.rejections) == 1
  input.reviews.older.rejections[0].state == "QUEUED"
  input.reviews.older.rejections[0].state_version == 1
 }`, role.ULID, *identity.ULID.Value, facts.At.UnixNano())
	for _, dialect := range []rego.EngineType{rego.V0, rego.V1} {
		t.Run(string(dialect), func(t *testing.T) {
			keyword := "approve "
			if dialect == rego.V1 {
				keyword = "approve if "
			}
			out, err := engine.Evaluate(
				t.Context(),
				rego.Request{Policy: rego.Policy{Type: rego.Approval, Engine: dialect, Body: "package infraweave\n" + keyword + body}, Input: raw},
			)
			if err != nil {
				t.Fatal(err)
			}
			var decision struct {
				Approve bool `json:"approve"`
			}
			if err := json.Unmarshal(out.Decision, &decision); err != nil || !decision.Approve {
				t.Fatalf("recorded review facts did not drive actual OPA approval: %s, %v; input %s", out.Decision, err, raw)
			}
		})
	}
}

func approvalReviewFacts() policyapi.RunInputFacts {
	facts := inputFacts()
	facts.Type = policyapi.Approval
	facts.Reviews = known(policyapi.ReviewsFacts{Current: policyapi.ReviewGroupFacts{Approvals: []policyapi.ReviewFacts{{
		Author: "reviewer", State: kernel.RunStateQueued, StateVersion: 1,
	}}}})
	return facts
}

func TestUnitApprovalReviewFactsRejectUnavailableMetadata(t *testing.T) {
	for name, mutate := range map[string]func(*policyapi.RunInputFacts){
		"author":                     func(f *policyapi.RunInputFacts) { f.Reviews.Value.Current.Approvals[0].Author = "" },
		"state":                      func(f *policyapi.RunInputFacts) { f.Reviews.Value.Current.Approvals[0].State = "" },
		"version":                    func(f *policyapi.RunInputFacts) { f.Reviews.Value.Current.Approvals[0].StateVersion = 0 },
		"unknown reviews with value": func(f *policyapi.RunInputFacts) { f.Reviews.Known = false },
		"null reviews":               func(f *policyapi.RunInputFacts) { f.Reviews.Value = nil },
		"unknown private note": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Note = policyapi.Fact[string]{Value: new("private-review-note")}
		},
		"request clock": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Request = known(policyapi.RequestFacts{})
		},
		"session teams unavailable": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Session = known(policyapi.SessionFacts{})
		},
		"invalid admin marker": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Session = known(policyapi.SessionFacts{Admin: known(policyapi.SessionAdmin(99)), Teams: known([]string{})})
		},
		"machine with human marker": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Session = known(policyapi.SessionFacts{Machine: true, Admin: known(policyapi.SessionIsAdmin), Teams: known([]string{})})
		},
		"human with machine marker": func(f *policyapi.RunInputFacts) {
			f.Reviews.Value.Current.Approvals[0].Session = known(policyapi.SessionFacts{Admin: known(policyapi.SessionMachineAdmin), Teams: known([]string{})})
		},
		"plan cannot silently drop reviews": func(f *policyapi.RunInputFacts) { f.Type = policyapi.Plan },
	} {
		t.Run(name, func(t *testing.T) {
			facts := approvalReviewFacts()
			mutate(&facts)
			_, err := (&Service{}).BuildRunInput(t.Context(), facts)
			if !errors.Is(err, policyapi.ErrInput) || strings.Contains(err.Error(), "private-review-note") {
				t.Fatalf("invalid review metadata did not fail safely: %v", err)
			}
		})
	}
}

func TestUnitApprovalReviewFactsPreserveNullAbsenceAndAdminVariants(t *testing.T) {
	for _, machine := range []bool{false, true} {
		t.Run(fmt.Sprint(machine), func(t *testing.T) {
			facts := approvalReviewFacts()
			review := &facts.Reviews.Value.Current.Approvals[0]
			review.AuthorRoles = policyapi.Fact[[]policyapi.RoleFacts]{Known: true}
			marker := policyapi.SessionNotAdmin
			if machine {
				marker = policyapi.SessionMachineAdmin
			}
			review.Session = known(
				policyapi.SessionFacts{Machine: machine, Admin: known(marker), Teams: policyapi.Fact[[]string]{Known: true}, StackID: policyapi.Fact[string]{Known: true}},
			)
			raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
			if err != nil {
				t.Fatal(err)
			}
			var input inputs.ApprovalInput
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatal(err)
			}
			reviews, _ := input.Reviews.Get()
			current, _ := reviews.Current.Get()
			older, _ := reviews.Older.Get()
			approvals, _ := current.Approvals.Get()
			rejected, _ := current.Rejections.Get()
			oldApprovals, _ := older.Approvals.Get()
			oldRejections, _ := older.Rejections.Get()
			if rejected == nil || oldApprovals == nil || oldRejections == nil || len(rejected)+len(oldApprovals)+len(oldRejections) != 0 {
				t.Fatalf("empty review groups became unknown/null: %s", raw)
			}
			got := approvals[0]
			session, _ := got.Session.Get()
			if !got.AuthorRoles.IsNull() || got.Identity.Present() || got.Request.Present() || got.Note.Present() || !session.Teams.IsNull() || !session.StackID.IsNull() ||
				session.IDPSubject.Present() {
				t.Fatalf("review null/absence changed: %s", raw)
			}
			admin, _ := session.Admin.Get()
			encoded, err := json.Marshal(admin)
			if err != nil {
				t.Fatal(err)
			}
			want := "false"
			if machine {
				want = `""`
			}
			if string(encoded) != want {
				t.Fatalf("session admin=%s,want%s", encoded, want)
			}
		})
	}
}
