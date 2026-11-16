package pg_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// managedState is a stacksapi.ManagedState that records each stack it is
// asked to open the default workspace of, whether the call came in a
// transaction, and the caller and object of each import, failing with
// err.
type managedState struct {
	created []kernel.ULID
	inTx    []bool
	imports []string
	err     error
}

func (m *managedState) Created(ctx context.Context, p kernel.Principal, st stacksapi.Stack, object string) error {
	m.created, m.inTx = append(m.created, st.ID), append(m.inTx, tx.In(ctx))
	if object != "" {
		m.imports = append(m.imports, p.Login+" "+object)
	}
	return m.err
}

// TestAccServiceManagedState checks that a stack whose state infraweave
// keeps is created whether or not the state domain is bound; that once it
// is, such a stack's creation asks it, in the creation's transaction, and
// a stack whose state is kept elsewhere's does not; and that a refusal
// fails the create, leaving no stack.
//
// ref: decision 0010 § "Storage"
func TestAccServiceManagedState(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	if _, err := f.svc.Create(ctx, f.admin, "", true, "", input("Unbound")); err != nil {
		t.Fatalf("a managed stack with no state domain bound = %v", err)
	}
	m := &managedState{}
	f.svc.BindManagedState(m)
	managed, err := f.svc.Create(ctx, f.admin, "", true, "", input("Managed"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create(ctx, f.admin, "", false, "", input("External")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.created, []kernel.ULID{managed.ID}) || !slices.Equal(m.inTx, []bool{true}) ||
		len(m.imports) != 0 {
		t.Errorf("the state domain was asked for %v, in a transaction %v, importing %v; want %s "+
			"in one", m.created, m.inTx, m.imports, managed.ID)
	}

	m.err = errors.New("down")
	if _, err := f.svc.Create(ctx, f.admin, "", true, "", input("Refused")); !errors.Is(err, m.err) {
		t.Errorf("a creation the state domain refuses = %v", err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "refused"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("the refused stack reads %v", err)
	}
}

// TestAccServiceImportAtCreation checks a stack's creation from an
// uploaded state file: without STACK_MANAGED_STATE_IMPORT on the new
// stack's space it is refused before the state domain is asked, a stack
// that keeps no managed state is refused pointing at stackObjectID, and
// otherwise the state domain imports the caller's object in the
// creation's transaction, its refusal leaving no stack. With no state
// domain bound, the object names no upload.
//
// ref: decision 0010 § "Upload, import and download URLs"
func TestAccServiceImportAtCreation(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.authzGrant(f.admin, f.team, accessapi.ActionStackManagedStateImport)
	if _, err := f.svc.Create(ctx, f.admin, "", true, "obj", input("Unbound")); !invalidAt(err,
		"/stackObjectID", kernel.ReasonNotFound) {
		t.Errorf("an import with no state domain bound = %v", err)
	}
	m := &managedState{}
	f.svc.BindManagedState(m)
	_, err := f.svc.Create(ctx, f.writer, "", true, "obj", input("Denied"))
	if d, ok := errors.AsType[*core.Denied](err); !ok ||
		d.Action != accessapi.ActionStackManagedStateImport || len(m.created) != 0 {
		t.Errorf("an import without STACK_MANAGED_STATE_IMPORT = %v, asking %v", err, m.created)
	}
	if _, err := f.svc.Create(ctx, f.admin, "", false, "obj", input("Plain")); !invalidAt(err,
		"/stackObjectID", kernel.ReasonRule) || len(m.created) != 0 {
		t.Errorf("an import into a stack without managed state = %v, asking %v", err, m.created)
	}
	// A Terragrunt stack's useStateManagement, not manageState, decides.
	off := input("Terragrunt Off")
	off.VendorConfig = json.RawMessage(`{"terragrunt":{"useStateManagement":false}}`)
	if _, err := f.svc.Create(ctx, f.admin, "", true, "obj", off); !invalidAt(err,
		"/stackObjectID", kernel.ReasonRule) || len(m.created) != 0 {
		t.Errorf("an import into an unmanaged Terragrunt stack = %v, asking %v", err, m.created)
	}
	st, err := f.svc.Create(ctx, f.admin, "", true, "obj", input("Imported"))
	if err != nil || !slices.Equal(m.created, []kernel.ULID{st.ID}) ||
		!slices.Equal(m.inTx, []bool{true}) || !slices.Equal(m.imports, []string{"admin obj"}) {
		t.Errorf("an import = %v, asking %v in a transaction %v, importing %v", err, m.created,
			m.inTx, m.imports)
	}

	on := input("Terragrunt On")
	on.VendorConfig = json.RawMessage(`{"terragrunt":{"useStateManagement":true}}`)
	tg, err := f.svc.Create(ctx, f.admin, "", false, "obj", on)
	if err != nil || !tg.ManagesState || !slices.Contains(m.created, tg.ID) {
		t.Errorf("an import into a managed Terragrunt stack = %v, asking %v", err, m.created)
	}

	m.err = &kernel.Invalid{Errors: []kernel.FieldError{{
		Pointer: "/stackObjectID", Reason: kernel.ReasonNotFound,
	}}}
	if _, err := f.svc.Create(ctx, f.admin, "", true, "gone", input("Missing")); !invalidAt(err,
		"/stackObjectID", kernel.ReasonNotFound) {
		t.Errorf("an import the state domain refuses = %v", err)
	}
	if _, err := f.svc.Stack(ctx, f.acct, "missing"); !errors.Is(err, stacksapi.ErrNotFound) {
		t.Errorf("the refused stack reads %v", err)
	}
	for _, slug := range []string{"unbound", "denied", "plain", "terragrunt-off"} {
		if _, err := f.svc.Stack(ctx, f.acct, slug); !errors.Is(err, stacksapi.ErrNotFound) {
			t.Errorf("the refused stack %s reads %v", slug, err)
		}
	}
}

// invalidAt reports whether err is a *kernel.Invalid whose first field
// is pointer, refused for reason.
func invalidAt(err error, pointer, reason string) bool {
	inv, ok := errors.AsType[*kernel.Invalid](err)
	return ok && len(inv.Errors) > 0 && inv.Errors[0].Pointer == pointer &&
		inv.Errors[0].Reason == reason
}
