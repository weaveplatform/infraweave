package assembly

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestAccBindingsPort binds a stack, as another domain does, reads one of
// its bindings, its bindings by actor and by space, refuses an unknown
// role and a binding it holds, moves and unbinds one under a check, and
// then unbinds the rest; it explains the stack as a run of it.
func TestAccBindingsPort(t *testing.T) {
	ctx := t.Context()
	d, db, a, dev := devSpace(t)
	p := claimed(t, d, db, a)
	port := d.Bindings()
	stack := kernel.NewULID()

	got, err := port.Bind(
		ctx,
		p,
		accessapi.ActorStack,
		"/roleBindings",
		[]accessapi.BindingRequest{
			{Actor: stack, Role: "space-reader", Space: dev.Slug},
			{Actor: stack, Role: "space-writer", Space: "guarded"},
		},
	)
	if err != nil || len(got) != 2 || got[0].Actor != accessapi.ActorStack ||
		got[0].ActorID != stack ||
		got[0].Role != "space-reader" ||
		got[0].SpaceID != dev.ID ||
		got[1].Space != "guarded" ||
		got[0].CreatedBy != p.Caller() || got[0].Version != 1 {
		t.Fatalf("bind = %+v, %v", got, err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, "/roleBindings",
		[]accessapi.BindingRequest{{Actor: stack, Role: "space-reader", Space: dev.Slug}})
	if !errors.Is(err, accessapi.ErrBindingHeld) {
		t.Errorf("a held binding = %v", err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, "",
		[]accessapi.BindingRequest{{Actor: stack, Role: "space-reader", Space: dev.Slug}})
	if !errors.Is(err, accessapi.ErrBindingHeld) {
		t.Errorf("a held binding of one = %v", err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, "/roleBindings",
		[]accessapi.BindingRequest{{Actor: stack, Role: "nope", Space: dev.Slug}})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || len(inv.Errors) != 1 ||
		inv.Errors[0].Pointer != "/roleBindings/0/roleID" {
		t.Errorf("an unknown role = %v", err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, "",
		[]accessapi.BindingRequest{{Actor: stack, Role: "nope", Space: dev.Slug}})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || len(inv.Errors) != 1 ||
		inv.Errors[0].Pointer != "/roleID" {
		t.Errorf("an unknown role of one = %v", err)
	}

	one, err := port.Binding(ctx, a.ID, accessapi.ActorStack, got[0].ID)
	if err != nil || one.ID != got[0].ID || one.Space != dev.Slug {
		t.Errorf("binding = %+v, %v", one, err)
	}
	if _, err := port.Binding(ctx, a.ID, "user", got[0].ID); !errors.Is(
		err, accessapi.ErrNoBinding) {
		t.Errorf("binding of another kind = %v", err)
	}
	mine, err := port.ByActor(ctx, a.ID, accessapi.ActorStack, stack)
	if err != nil || len(mine) != 2 {
		t.Errorf("by actor = %+v, %v", mine, err)
	}
	inDev, err := port.BySpace(ctx, a.ID, dev.ID)
	if err != nil || len(inDev) != 1 || inDev[0].ID != got[0].ID {
		t.Errorf("by space = %+v, %v", inDev, err)
	}

	explained, err := d.Grants().Stacks(ctx, a.ID, dev.ID, []accessapi.StackHome{
		{Stack: stack, Space: a.ID.ULID}, {Stack: kernel.NewULID(), Space: dev.ID},
	})
	if err != nil || len(explained) != 2 || len(explained[0]) != 3 ||
		explained[0][0].Source != "RESIDENT" || explained[0][0].Binding != got[0].ID ||
		explained[0][2].Source != "PROPAGATED" || !explained[0][2].Binding.IsZero() ||
		len(explained[1]) != 1 || explained[1][0].Role != "space-reader" {
		t.Errorf("grants = %+v, %v", explained, err)
	}

	refused := errors.New("refused")
	var seen accessapi.RoleBinding
	moved, err := port.Rebind(ctx, p, accessapi.ActorStack, got[0].ID, kernel.ExpectVersions(1),
		accessapi.BindingRequest{Role: "space-admin", Space: dev.Slug},
		func(b accessapi.RoleBinding) error { seen = b; return nil })
	if err != nil || moved.Role != "space-admin" || moved.Version != 2 ||
		seen.Role != "space-reader" {
		t.Errorf("rebind = %+v, %v; checked %+v", moved, err, seen)
	}
	if _, err := port.Rebind(ctx, p, accessapi.ActorStack, got[0].ID, kernel.Expect{},
		accessapi.BindingRequest{Role: "space-writer", Space: "guarded"}, nil); !errors.Is(
		err, accessapi.ErrBindingHeld) {
		t.Errorf("rebind onto a held binding = %v", err)
	}
	if _, err := port.Rebind(ctx, p, accessapi.ActorStack, kernel.NewULID(), kernel.Expect{},
		accessapi.BindingRequest{Role: "space-admin", Space: dev.Slug}, nil); !errors.Is(
		err, accessapi.ErrNoBinding) {
		t.Errorf("rebind of none = %v", err)
	}
	if _, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, got[0].ID, kernel.Expect{},
		func(accessapi.RoleBinding) error { return refused }); !errors.Is(err, refused) {
		t.Errorf("a refused unbind = %v", err)
	}
	gone, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, got[0].ID, kernel.Expect{}, nil)
	if err != nil || gone.ID != got[0].ID {
		t.Errorf("unbind = %+v, %v", gone, err)
	}
	if _, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, got[0].ID, kernel.Expect{},
		nil); !errors.Is(err, accessapi.ErrNoBinding) {
		t.Errorf("unbind of none = %v", err)
	}
	if err := port.UnbindActor(ctx, a.ID, accessapi.ActorStack, stack); err != nil {
		t.Fatal(err)
	}
	if mine, err := port.ByActor(
		ctx,
		a.ID,
		accessapi.ActorStack,
		stack,
	); err != nil ||
		len(mine) != 0 {
		t.Errorf("after unbinding the actor = %+v, %v", mine, err)
	}

	db.Primary().Close()
	d.authz.Flush()
	for name, err := range map[string]error{
		"bind": func() error {
			_, err := port.Bind(ctx, p, accessapi.ActorStack, "",
				[]accessapi.BindingRequest{{Actor: stack, Role: "space-reader", Space: dev.Slug}})
			return err
		}(),
		"binding": func() error {
			_, err := port.Binding(ctx, a.ID, accessapi.ActorStack, stack)
			return err
		}(),
		"unbind": func() error {
			_, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, stack, kernel.Expect{}, nil)
			return err
		}(),
		"actor":   func() error { _, err := port.ByActor(ctx, a.ID, accessapi.ActorStack, stack); return err }(),
		"space":   func() error { _, err := port.BySpace(ctx, a.ID, dev.ID); return err }(),
		"unbinds": port.UnbindActor(ctx, a.ID, accessapi.ActorStack, stack),
		"grants": func() error {
			_, err := d.Grants().Stacks(ctx, a.ID, dev.ID, nil)
			return err
		}(),
	} {
		if err == nil || errors.Is(err, accessapi.ErrNoBinding) {
			t.Errorf("%s with PostgreSQL closed = %v", name, err)
		}
	}
}

// TestAccBindingSubjects checks that the Requirer finds a binding's space
// by its ID under its own kind, hiding one of another kind, an unknown ID
// and one that is no ULID.
func TestAccBindingSubjects(t *testing.T) {
	ctx := t.Context()
	d, db, a, dev := devSpace(t)
	p := claimed(t, d, db, a)
	ls, err := d.bindings.Create(ctx, p, binding.ActorUser, "", binding.Fields{}, []binding.Request{
		{Actor: p.MemberID.String(), Role: "space-reader", Space: dev.Slug},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := ls[0].ID.String()
	for _, c := range []struct {
		entity, id string
		code       int
	}{
		{"user_role_binding", id, http.StatusOK},
		{"api_key_role_binding", id, http.StatusNotFound},
		{"user_group_role_binding", kernel.NewULID().String(), http.StatusNotFound},
		{"user_role_binding", "nope", http.StatusNotFound},
	} {
		h := d.Requirer().Middleware(func(*http.Request) (accessapi.Required, bool) {
			return accessapi.Required{
				Action: accessapi.ActionSpaceAdmin,
				Subject: accessapi.Subject{
					Kind:   accessapi.SubjectPath,
					Param:  "id",
					Entity: c.entity,
				},
				Params: map[string]string{"id": c.id},
			}, true
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(kernel.WithPrincipal(ctx, p),
			http.MethodGet, "/", nil))
		if rec.Code != c.code {
			t.Errorf("%s %q: %d %s, want %d", c.entity, c.id, rec.Code, rec.Body, c.code)
		}
	}
}
