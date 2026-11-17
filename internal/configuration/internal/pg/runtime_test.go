package pg_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// runtimeReads counts the metadata batches while using the real store.
// Opened panics because this view cannot open secrets.
type runtimeReads struct {
	core.Store
	attachments, contexts, elements int
	owners                          []core.Owner
	ids                             []kernel.ULID
}

func (s *runtimeReads) Attachments(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID, byStack bool) (map[kernel.ULID][]core.Attachment, error) {
	s.attachments++
	return s.Store.Attachments(ctx, acct, ids, byStack)
}

func (s *runtimeReads) ByIDs(ctx context.Context, acct kernel.AccountID, ids []kernel.ULID) (map[kernel.ULID]core.Context, error) {
	s.contexts++
	s.ids = slices.Clone(ids)
	return s.Store.ByIDs(ctx, acct, ids)
}

func (s *runtimeReads) Elements(ctx context.Context, acct kernel.AccountID, owners []core.Owner) (map[kernel.ULID][]core.Element, error) {
	s.elements++
	s.owners = slices.Clone(owners)
	return s.Store.Elements(ctx, acct, owners)
}

func (*runtimeReads) Opened(context.Context, kernel.AccountID, []core.Owner) (map[kernel.ULID][]core.Opened, error) {
	panic("runtimeConfig must not open secrets")
}

// TestAccRuntimeConfig resolves stored winners, preserving source metadata,
// without opening secrets; empty stacks still list every computed name.
func TestAccRuntimeConfig(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, e := range []core.ElementInput{
		variable("LEVEL", "stack"),
		variable("TF_VAR_infraweave_run_type", "stored"),
		variable(kernel.RunStateVariable, "stored-state"),
		variable(kernel.APITokenVariable, "stored-token"),
	} {
		if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", e, "", "/configId"); err != nil {
			t.Fatal(err)
		}
	}
	direct, err := f.svc.Create(ctx, f.admin, core.Input{
		Name: "Shared", Space: "team", Description: new("shared config"), Labels: []string{"shared"},
		Hooks: map[string][]string{stacksapi.BeforePlan: {"echo shared"}},
		Config: []core.ElementInput{
			variable("LEVEL", "context"), variable("DIRECT", "direct"),
			{Name: "TOKEN", Value: new("secret"), WriteOnly: true, Description: new("credential")},
			{Name: "a.txt", FileMount: true, Value: new("aGVsbG8="), FileMode: new("0640"), Description: new("file")},
		},
		Stacks: []core.StackInput{{Stack: "web", Priority: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Zulu"} {
		if _, err := f.svc.Create(ctx, f.admin, core.Input{
			Name: name, Space: "team", Labels: []string{"autoattach:*"},
			Config: []core.ElementInput{variable("DIRECT", name), variable("AUTO", name)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	attached, err := f.svc.AttachedContexts(ctx, f.acct, []kernel.ULID{f.web.ID})
	if err != nil {
		t.Fatal(err)
	}
	reads := &runtimeReads{Store: f.store}
	svc := core.New(core.Deps{Store: reads})
	var first []stacksapi.ConfigElementWithSource
	for _, ids := range [][]kernel.ULID{{f.web.ID}, {f.web.ID, f.data.ID}} {
		reads.attachments, reads.contexts, reads.elements = 0, 0, 0
		got, err := svc.RuntimeConfig(ctx, f.acct, ids)
		if err != nil {
			t.Fatal(err)
		}
		if reads.attachments != 1 || reads.contexts != 1 || reads.elements != 1 || len(reads.ids) != 3 || len(reads.owners) != len(ids)+3 {
			t.Fatalf("batch %d: reads %d/%d/%d, contexts %v, owners %v", len(ids), reads.attachments, reads.contexts, reads.elements, reads.ids, reads.owners)
		}
		if first != nil && !reflect.DeepEqual(first, got[f.web.ID]) {
			t.Error("batch changes the stack's resolved configuration")
		}
		first = got[f.web.ID]
		if len(ids) == 2 {
			assertComputed(t, got[f.data.ID])
		}
	}
	byName := map[string]stacksapi.ConfigElementWithSource{}
	names := make([]string, 0, len(first))
	for _, e := range first {
		if _, exists := byName[e.ID]; exists {
			t.Errorf("duplicate name %s", e.ID)
		}
		byName[e.ID] = e
		names = append(names, e.ID)
		if e.WriteOnly && e.Value != nil {
			t.Errorf("write-only %s has a value", e.ID)
		}
		if e.Context != nil {
			found := false
			for _, a := range attached[f.web.ID] {
				found = found || reflect.DeepEqual(a, *e.Context)
			}
			if !found {
				t.Errorf("%s has a different attachment: %+v", e.ID, e.Context)
			}
		}
	}
	if !slices.IsSorted(names) || len(names) != len(kernel.ComputedEnvironmentNames())+5 {
		t.Errorf("resolved names %v", names)
	}
	for name, want := range map[string]string{
		"LEVEL": "stack", "DIRECT": "direct", "AUTO": "Zulu", "a.txt": "aGVsbG8=",
		"TF_VAR_infraweave_run_type": "stored", kernel.APITokenVariable: "stored-token", kernel.RunStateVariable: "stored-state",
	} {
		e := byName[name]
		if e.Value == nil || *e.Value != want || e.Runtime || e.CreatedAt.IsZero() || e.Checksum == "" {
			t.Errorf("stored %s = %+v", name, e)
		}
	}
	if byName["LEVEL"].Context != nil || byName[kernel.APITokenVariable].Context != nil || byName[kernel.RunStateVariable].Context != nil {
		t.Error("a stack winner carries a context")
	}
	if e := byName["DIRECT"]; e.Context == nil || e.Context.Context != direct.Slug || e.Context.Autoattached {
		t.Errorf("direct winner %+v", e)
	}
	if e := byName["AUTO"]; e.Context == nil || e.Context.Context != "zulu" || !e.Context.Autoattached {
		t.Errorf("auto winner %+v", e)
	}
	if e := byName["TOKEN"]; !e.WriteOnly || e.Value != nil || e.Description != "credential" || e.Checksum != sum("secret") || e.CreatedAt.IsZero() {
		t.Errorf("secret metadata %+v", e)
	}
	if e := byName["a.txt"]; !e.FileMount || e.FileMode == nil || *e.FileMode != "0640" || e.Description != "file" {
		t.Errorf("file metadata %+v", e)
	}
}

func assertComputed(t *testing.T, es []stacksapi.ConfigElementWithSource) {
	t.Helper()
	names := make([]string, 0, len(es))
	for _, e := range es {
		names = append(names, e.ID)
		if !e.Runtime || e.Context != nil || e.Value != nil || !e.CreatedAt.IsZero() || e.FileMode != nil || e.FileMount || e.Checksum != "" || e.Description != "" ||
			e.WriteOnly != (e.ID == kernel.APITokenVariable) {
			t.Errorf("computed element %+v", e)
		}
	}
	if !slices.Equal(names, kernel.ComputedEnvironmentNames()) {
		t.Errorf("computed names %v", names)
	}
}
