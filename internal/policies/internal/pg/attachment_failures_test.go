package pg_test

import (
	"errors"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func TestAccAutomaticAttachmentPortFailuresRollback(t *testing.T) {
	for _, port := range []string{"InSpaces", "Find", "Hold", "Get", "Labels", "Tree"} {
		t.Run(port, func(t *testing.T) {
			f := newFixture(t)
			down := errors.New("injected automatic target failure")
			f.stacks.fail, f.stacks.err = port, down
			if port == "Tree" {
				f.spaces.treeErr = down
			}
			labels := []string{"autoattach:*"}
			if port == "Find" {
				labels = []string{"autoattach:prod"}
			}
			_, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Must roll back", Type: rego.Plan, Body: "package infraweave\n", Labels: labels})
			if !errors.Is(err, down) || f.count(t, "policies") != 0 || f.count(t, "policy_stack_attachments") != 0 || f.count(t, "events") != 0 {
				t.Fatalf("automatic port failure partially committed: %v", err)
			}
		})
	}
	f := newFixture(t)
	f.stacks.fail, f.stacks.err = "Hold", stacksapi.ErrNotFound
	if _, err := f.svc.Create(
		t.Context(),
		f.principal,
		core.Input{Name: "Deleted target", Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"autoattach:*"}},
	); err != nil ||
		f.count(t, "policy_stack_attachments") != 0 {
		t.Fatalf("deleted discovery target was attached: %v", err)
	}
	if err := f.svc.StackChanged(t.Context(), f.principal.AccountID, f.stacks.stack.ID); err != nil {
		t.Fatalf("deleted stack delivery: %v", err)
	}
}

func TestAccExplicitAttachmentPortFailuresRollback(t *testing.T) {
	for _, port := range []string{"BySlug", "Hold", "Get", "Tree", "SpaceHold", "Event", "Authz"} {
		t.Run(port, func(t *testing.T) {
			f := newFixture(t)
			p := f.create(t, "Stable")
			down := errors.New("injected attachment failure")
			f.stacks.fail, f.stacks.err = port, down
			if port == "Tree" {
				f.spaces.treeErr = down
			}
			if port == "SpaceHold" {
				f.spaces.holdErr = down
			}
			if port == "Authz" {
				f.authz.err = down
			}
			svc := f.svc
			if port == "Event" {
				svc = f.service(failedEvents{err: down})
			}
			_, _, err := svc.Attach(t.Context(), f.principal, p.ID.String(), "infra")
			stored, readErr := f.store.Get(t.Context(), p.Account, p.ID, false)
			if !errors.Is(err, down) || readErr != nil || stored.Version != p.Version || f.count(t, "policy_stack_attachments") != 0 || f.count(t, "events") != 1 {
				t.Fatalf("failed attachment partially committed: %+v %v %v", stored, err, readErr)
			}
		})
	}
}

func TestAccDetachAndProjectionPortFailuresPreserveRelation(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Stable")
	a, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), "infra")
	if err != nil {
		t.Fatal(err)
	}
	down := errors.New("injected attachment read failure")
	for _, port := range []string{"Hold", "Get", "Authz", "Event"} {
		f.stacks.fail, f.stacks.err = port, down
		if port == "Authz" {
			f.authz.err = down
		}
		svc := f.svc
		if port == "Event" {
			svc = f.service(failedEvents{err: down})
		}
		_, _, err := svc.Detach(t.Context(), f.principal, a.ID.String())
		if !errors.Is(err, down) || f.count(t, "policy_stack_attachments") != 1 || f.count(t, "events") != 2 {
			t.Fatalf("failed detach removed relation: %s %v", port, err)
		}
		f.authz.err = nil
	}
	f.stacks.fail = ""
	for _, port := range []string{"Labels", "Tree"} {
		f.stacks.fail, f.stacks.err = port, down
		if port == "Tree" {
			f.spaces.treeErr = down
		}
		for name, call := range map[string]func() error{
			"parent": func() error {
				_, _, err := f.svc.PolicyAttachment(t.Context(), f.principal, p.ID.String(), a.ID.String())
				return err
			},
			"target": func() error {
				_, _, err := f.svc.StackAttachment(t.Context(), f.principal, "infra", a.ID.String())
				return err
			},
			"details": func() error { _, err := f.svc.Details(t.Context(), f.principal, p); return err },
			"selection": func() error {
				_, err := f.svc.AttachedPolicies(t.Context(), f.principal, []kernel.ULID{a.Stack})
				return err
			},
		} {
			if err := call(); !errors.Is(err, down) {
				t.Errorf("%s lost %s failure: %v", name, port, err)
			}
		}
		f.spaces.treeErr = nil
	}
	f.stacks.fail, f.stacks.err = "ByIDs", down
	if _, err := f.svc.Details(t.Context(), f.principal, p); !errors.Is(err, down) {
		t.Fatalf("details hid target read failure: %v", err)
	}
	if _, err := f.svc.AttachedPolicies(t.Context(), f.principal, []kernel.ULID{a.Stack}); !errors.Is(err, down) {
		t.Fatalf("selection hid target read failure: %v", err)
	}
	f.stacks.fail = ""
	f.authz.reachErr = down
	if _, err := f.svc.Details(t.Context(), f.principal, p); !errors.Is(err, down) {
		t.Fatalf("details hid readable-space failure: %v", err)
	}
	if _, err := f.svc.AttachedPolicies(t.Context(), f.principal, []kernel.ULID{a.Stack}); !errors.Is(err, down) {
		t.Fatalf("selection hid readable-space failure: %v", err)
	}
}

func TestAccStackReconcileFailuresCommitNoSelection(t *testing.T) {
	for _, port := range []string{"Hold", "Get", "Tree", "Labels", "InSpaces", "Event"} {
		t.Run(port, func(t *testing.T) {
			f := newFixture(t)
			p := f.create(t, "Awaiting reconciliation")
			p.Labels = []string{"autoattach:*"}
			if err := f.store.Update(t.Context(), p); err != nil {
				t.Fatal(err)
			}
			down := errors.New("injected reconcile failure")
			f.stacks.fail, f.stacks.err = port, down
			if port == "Tree" {
				f.spaces.treeErr = down
			}
			svc := f.svc
			if port == "Event" {
				svc = f.service(failedEvents{err: down})
			}
			err := svc.StackChanged(t.Context(), p.Account, f.stacks.stack.ID)
			stored, readErr := f.store.Get(t.Context(), p.Account, p.ID, false)
			if !errors.Is(err, down) || readErr != nil || stored.Version != p.Version || f.count(t, "policy_stack_attachments") != 0 || f.count(t, "events") != 1 {
				t.Fatalf("failed reconcile committed state: %+v %v %v", stored, err, readErr)
			}
		})
	}
}

func TestAccPreviewRefusesDependencyFailures(t *testing.T) {
	f := newFixture(t)
	down := errors.New("injected preview failure")
	for _, port := range []string{"Find", "Read", "Tree", "Reachable", "Preview"} {
		switch port {
		case "Find":
			f.spaces.err = down
		case "Read":
			f.authz.err = down
		case "Tree":
			f.spaces.treeErr = down
		case "Reachable":
			f.authz.reachErr = down
		case "Preview":
			f.stacks.fail, f.stacks.err = port, down
		}
		if _, err := f.svc.Preview(t.Context(), f.principal, "root", []string{"autoattach:*"}); !errors.Is(err, down) {
			t.Fatalf("preview hid %s failure: %v", port, err)
		}
		f.spaces.err, f.spaces.treeErr, f.authz.err, f.authz.reachErr = nil, nil, nil, nil
		f.stacks.fail = ""
	}
	if _, err := f.svc.Preview(t.Context(), f.principal, "root", []string{"duplicate", "duplicate"}); err == nil {
		t.Fatal("duplicate preview labels accepted")
	}
}

func TestAccAttachmentMoveAndCandidateFailures(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Movable")
	if _, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), "infra"); err != nil {
		t.Fatal(err)
	}
	down := errors.New("injected structural dependency failure")
	for _, port := range []string{"Hold", "Get", "Tree"} {
		f.stacks.fail, f.stacks.err = port, down
		if port == "Tree" {
			f.spaces.treeErr = down
		}
		_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, core.Input{Name: p.Name, Body: p.Body, Space: "team"})
		stored, readErr := f.store.Get(t.Context(), p.Account, p.ID, false)
		if !errors.Is(err, down) || readErr != nil || stored.Space != p.Space || stored.Version != 2 {
			t.Fatalf("failed policy move committed: %+v %v %v", stored, err, readErr)
		}
		f.spaces.treeErr = nil
	}
	f.stacks.fail = ""
	f.spaces.slugsErr = down
	if _, err := f.svc.StackCandidates(t.Context(), f.principal, "infra", search.Input{}); !errors.Is(err, down) {
		t.Fatalf("candidates hid naming failure: %v", err)
	}
	f.spaces.slugsErr = nil
	for _, port := range []string{"ByIDs", "Tree"} {
		f.stacks.fail, f.stacks.err = port, down
		if port == "Tree" {
			f.spaces.treeErr = down
		}
		if err := f.svc.SpaceChanging(
			t.Context(),
			accessapi.SpaceChange{Account: p.Account, Space: f.spaces.nodes[1].ID, Parent: p.Space, Inherit: false, Version: 2},
		); !errors.Is(
			err,
			down,
		) {
			t.Fatalf("space guard hid %s failure: %v", port, err)
		}
		if port == "Tree" {
			if _, err := f.svc.StackGuard().LostOnMove(t.Context(), stacksapi.StackMove{Stack: f.stacks.stack, To: f.spaces.nodes[1].ID}); !errors.Is(err, down) {
				t.Fatalf("stack guard hid %s failure: %v", port, err)
			}
		}
		f.spaces.treeErr = nil
	}
}

func TestAccStaleAutomaticAndMismatchedParentsStayHidden(t *testing.T) {
	f := newFixture(t)
	p, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Guard", Type: rego.Plan, Body: "package infraweave\n", Labels: []string{"autoattach:*"}})
	if err != nil {
		t.Fatal(err)
	}
	all, err := f.store.Attachments(t.Context(), p.Account, []kernel.ULID{p.ID}, false)
	if err != nil || len(all[p.ID]) != 1 || !all[p.ID][0].Autoattached {
		t.Fatalf("automatic fixture: %+v %v", all, err)
	}
	a := all[p.ID][0]
	other := f.create(t, "Other parent")
	if _, _, err := f.svc.PolicyAttachment(t.Context(), f.principal, other.ID.String(), a.ID.String()); err == nil {
		t.Fatal("attachment was readable under a different policy")
	}
	// Model the period after a committed reach change and before its durable
	// cleanup delivery. The owned row still exists, but is no longer effective.
	f.stacks.stack.Space = f.spaces.nodes[1].ID
	f.spaces.nodes[1].Inherit = false
	for name, call := range map[string]func() error{
		"parent": func() error {
			_, _, err := f.svc.PolicyAttachment(t.Context(), f.principal, p.ID.String(), a.ID.String())
			return err
		},
		"target": func() error {
			_, _, err := f.svc.StackAttachment(t.Context(), f.principal, "infra", a.ID.String())
			return err
		},
	} {
		var missing *core.Missing
		if err := call(); !errors.As(err, &missing) {
			t.Fatalf("%s exposed stale relation: %v", name, err)
		}
	}
	details, err := f.svc.Details(t.Context(), f.principal, p)
	if err != nil || len(details.Attachments[p.ID]) != 0 {
		t.Fatalf("stale policy projection: %+v %v", details, err)
	}
	selected, err := f.svc.AttachedPolicies(t.Context(), f.principal, []kernel.ULID{a.Stack})
	if err != nil || len(selected[a.Stack]) != 0 || f.count(t, "policy_stack_attachments") != 1 {
		t.Fatalf("stale selected collection: %+v %v", selected, err)
	}
	f.stacks.stack.ID = kernel.NewULID()
	if _, _, err := f.svc.StackAttachment(t.Context(), f.principal, "infra", a.ID.String()); err == nil {
		t.Fatal("attachment was readable under a different stack")
	}
}

func TestAccDisappearingStackIsConcealed(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Guard")
	for _, port := range []string{"BySlug", "Hold", "Get"} {
		f.stacks.fail, f.stacks.err = port, stacksapi.ErrNotFound
		var missing *core.Missing
		if _, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), "infra"); !errors.As(err, &missing) {
			t.Fatalf("attach exposed disappearing %s target as an internal error: %v", port, err)
		}
		if f.count(t, "policy_stack_attachments") != 0 || f.count(t, "events") != 1 {
			t.Fatal("disappearing target committed an attachment")
		}
	}
	f.stacks.fail = ""
	a, _, err := f.svc.Attach(t.Context(), f.principal, p.ID.String(), "infra")
	if err != nil {
		t.Fatal(err)
	}
	for _, port := range []string{"Get", "Hold"} {
		f.stacks.fail, f.stacks.err = port, stacksapi.ErrNotFound
		var missing *core.Missing
		if _, _, err := f.svc.Detach(t.Context(), f.principal, a.ID.String()); !errors.As(err, &missing) {
			t.Fatalf("detach exposed disappearing %s target as an internal error: %v", port, err)
		}
		if f.count(t, "policy_stack_attachments") != 1 || f.count(t, "events") != 2 {
			t.Fatal("failed detach partially committed")
		}
	}
}
