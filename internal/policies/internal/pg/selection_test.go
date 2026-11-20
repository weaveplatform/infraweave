package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	policyapi "github.com/weaveplatform/infraweave/internal/policies/api"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccPolicyPresenceSharesEffectiveSelectionWithoutRuntimeOrEvidence(t *testing.T) {
	f := newFixture(t)
	f.stacks.stack.Space = f.spaces.nodes[1].ID
	request := policyapi.SelectionRequest{Account: f.principal.AccountID, Type: policyapi.Push, Target: policyapi.Target{Kind: policyapi.StackTarget, ID: f.stacks.stack.ID}}
	// This reader has no runtime or event sink; invalid stored bodies below also
	// make an accidental evaluator call observable without inspecting OPA internals.
	reader := core.New(core.Deps{DB: f.db, Store: f.store, Stacks: f.stacks, Spaces: f.spaces})
	assert := func(want bool) {
		t.Helper()
		before := f.count(t, "events")
		got, err := reader.HasPolicies(t.Context(), request)
		if err != nil || got != want || f.count(t, "events") != before {
			t.Fatalf("presence=%t want=%t error=%v", got, want, err)
		}
	}
	assert(false)
	p, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Inherited push", Type: rego.Push, Body: "package infraweave\ntrack := true", Labels: []string{"autoattach:prod"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	assert(true)
	f.stacks.labels = []string{"dev"}
	assert(false)
	f.stacks.labels = []string{"prod"}
	f.spaces.nodes[1].Inherit = false
	assert(false)
	f.spaces.nodes[1].Inherit = true
	if _, err := f.db.Exec(t.Context(), `UPDATE policies SET body='invalid source must not be compiled by presence' WHERE account_id=$1 AND id=$2`, p.Account, p.ID); err != nil {
		t.Fatal(err)
	}
	assert(true)
	if err := f.store.Delete(t.Context(), p.Account, p.ID); err != nil {
		t.Fatal(err)
	}
	assert(false)
	p, err = f.svc.Create(t.Context(), f.principal, core.Input{Name: "Explicit push", Type: rego.Push, Body: "package infraweave\npropose := true"})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), f.stacks.stack.Slug)
	if err != nil {
		t.Fatal(err)
	}
	f.stacks.labels = []string{"dev"}
	assert(true)
	abort := errors.New("rollback effective presence read")
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		if err := f.store.DeleteStack(ctx, p.Account, f.stacks.stack.ID); err != nil {
			return err
		}
		got, err := reader.HasPolicies(ctx, request)
		if err != nil {
			return err
		}
		if got {
			return fmt.Errorf("presence did not join caller transaction")
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	assert(true)
	if _, _, err := f.svc.Detach(t.Context(), f.principal, a.ID.String()); err != nil {
		t.Fatal(err)
	}
	assert(false)
	foreign := p
	foreign.Account, foreign.ID = kernel.NewAccountID(), kernel.NewULID()
	foreign.Labels = []string{"autoattach:*"}
	if err := f.store.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	assert(false)
	for _, scope := range []string{"account", "target"} {
		r := request
		if scope == "account" {
			r.Account = foreign.Account
		} else {
			r.Target.ID = kernel.NewULID()
		}
		got, err := reader.HasPolicies(t.Context(), r)
		if got || !errors.Is(err, stacksapi.ErrNotFound) {
			t.Fatalf("foreign %s presence=%t error=%v", scope, got, err)
		}
	}
	for _, boundary := range []string{"Hold", "Get", "Labels", "space hold", "tree"} {
		t.Run(boundary, func(t *testing.T) {
			cause := errors.New("selection owner failed")
			switch boundary {
			case "space hold":
				f.spaces.holdErr = cause
			case "tree":
				f.spaces.treeErr = cause
			default:
				f.stacks.fail, f.stacks.err = boundary, cause
			}
			got, err := reader.HasPolicies(t.Context(), request)
			f.spaces.holdErr, f.spaces.treeErr = nil, nil
			f.stacks.fail, f.stacks.err = "", nil
			if got || !errors.Is(err, cause) {
				t.Fatalf("owner failure became absence: presence=%t error=%v", got, err)
			}
		})
	}
	err = tx.Do(t.Context(), f.db, tx.Options{}, func(ctx context.Context) error {
		var pid int32
		if err := tx.Q(ctx, f.db).QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		if _, err := f.db.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			return err
		}
		_, err := reader.HasPolicies(ctx, request)
		return err
	})
	if !errors.Is(err, kernel.ErrUnavailable) {
		t.Fatalf("presence DB failure=%v", err)
	}
	for _, change := range []func(*policyapi.SelectionRequest){
		func(r *policyapi.SelectionRequest) { r.Account = kernel.AccountID{} }, func(r *policyapi.SelectionRequest) { r.Target.ID = kernel.ULID{} },
		func(r *policyapi.SelectionRequest) { r.Type = policyapi.Intent }, func(r *policyapi.SelectionRequest) { r.Target.Kind = policyapi.ModuleTarget },
	} {
		r := request
		change(&r)
		if got, err := reader.HasPolicies(t.Context(), r); got || !errors.Is(err, policyapi.ErrTarget) {
			t.Fatalf("invalid selection: presence=%t error=%v", got, err)
		}
	}
}
