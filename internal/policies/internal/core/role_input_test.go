package core

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func TestUnitRunInputPreservesStackRolePublicAndImmutableIDs(t *testing.T) {
	for _, kind := range []policyapi.Type{policyapi.Plan, policyapi.Approval} {
		t.Run(string(kind), func(t *testing.T) {
			facts := inputFacts()
			facts.Type = kind
			id := kernel.NewULID()
			facts.Stack.Roles = known([]policyapi.RoleFacts{{ID: id, Slug: "deployer", Name: "Deployment role"}})
			raw, err := (&Service{}).BuildRunInput(t.Context(), facts)
			if err != nil {
				t.Fatal(err)
			}
			var stack inputs.Stack
			if kind == policyapi.Plan {
				var document inputs.PlanInput
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				context, _ := document.Infraweave.Get()
				stack, _ = context.Stack.Get()
			} else {
				var document inputs.ApprovalInput
				if err := json.Unmarshal(raw, &document); err != nil {
					t.Fatal(err)
				}
				stack, _ = document.Stack.Get()
			}
			roles, _ := stack.Roles.Get()
			if len(roles) != 1 {
				t.Fatalf("stack roles disappeared: %s", raw)
			}
			public, _ := roles[0].ID.Get()
			slug, _ := roles[0].Slug.Get()
			name, _ := roles[0].Name.Get()
			immutable, _ := roles[0].ULID.Get()
			if public != "deployer" || slug != public || name != "Deployment role" || immutable != id.String() || immutable == public {
				t.Fatalf("role identifiers changed: %s", raw)
			}
		})
	}
}

func TestUnitRunInputRefusesInvalidStackRoleFacts(t *testing.T) {
	for _, reason := range []string{"ID", "slug", "name", "contradictory", "null"} {
		t.Run(reason, func(t *testing.T) {
			facts := inputFacts()
			roles := []policyapi.RoleFacts{{ID: kernel.NewULID(), Slug: "deployer", Name: "Deployment"}}
			facts.Stack.Roles = known(roles)
			switch reason {
			case "ID":
				roles[0].ID = kernel.ULID{}
			case "slug":
				roles[0].Slug = ""
			case "name":
				roles[0].Name = ""
			case "contradictory":
				facts.Stack.Roles.Known = false
			case "null":
				facts.Stack.Roles.Value = nil
			}
			if raw, err := (&Service{}).BuildRunInput(t.Context(), facts); !errors.Is(err, policyapi.ErrInput) || raw != nil {
				t.Fatalf("invalid roles produced input=%s error=%v", raw, err)
			}
		})
	}
}
