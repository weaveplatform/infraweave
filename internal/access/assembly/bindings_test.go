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

// TestAccBindingsPort binds a stack, as another domain does, reads its
// bindings by actor and by space, refuses an unknown role and a binding
// it holds, and unbinds one and then the rest.
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
		stack,
		"/roleBindings",
		[]accessapi.BindingRequest{
			{Role: "space-reader", Space: dev.Slug}, {Role: "space-writer", Space: "guarded"},
		},
	)
	if err != nil || len(got) != 2 || got[0].Actor != accessapi.ActorStack ||
		got[0].ActorID != stack ||
		got[0].Role != "space-reader" ||
		got[0].SpaceID != dev.ID ||
		got[1].Space != "guarded" ||
		got[0].CreatedBy != p.Caller() {
		t.Fatalf("bind = %+v, %v", got, err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, stack, "/roleBindings",
		[]accessapi.BindingRequest{{Role: "space-reader", Space: dev.Slug}})
	if !errors.Is(err, accessapi.ErrBindingHeld) {
		t.Errorf("a held binding = %v", err)
	}
	_, err = port.Bind(ctx, p, accessapi.ActorStack, stack, "/roleBindings",
		[]accessapi.BindingRequest{{Role: "nope", Space: dev.Slug}})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || len(inv.Errors) != 1 ||
		inv.Errors[0].Pointer != "/roleBindings/0/roleID" {
		t.Errorf("an unknown role = %v", err)
	}

	mine, err := port.ByActor(ctx, a.ID, accessapi.ActorStack, stack)
	if err != nil || len(mine) != 2 {
		t.Errorf("by actor = %+v, %v", mine, err)
	}
	inDev, err := port.BySpace(ctx, a.ID, dev.ID)
	if err != nil || len(inDev) != 1 || inDev[0].ID != got[0].ID {
		t.Errorf("by space = %+v, %v", inDev, err)
	}
	gone, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, got[0].ID)
	if err != nil || gone.ID != got[0].ID {
		t.Errorf("unbind = %+v, %v", gone, err)
	}
	if _, err := port.Unbind(
		ctx,
		a.ID,
		accessapi.ActorStack,
		got[0].ID,
	); !errors.Is(
		err,
		accessapi.ErrNoBinding,
	) {
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
	for name, err := range map[string]error{
		"bind": func() error {
			_, err := port.Bind(ctx, p, accessapi.ActorStack, stack, "",
				[]accessapi.BindingRequest{{Role: "space-reader", Space: dev.Slug}})
			return err
		}(),
		"unbind":  func() error { _, err := port.Unbind(ctx, a.ID, accessapi.ActorStack, stack); return err }(),
		"actor":   func() error { _, err := port.ByActor(ctx, a.ID, accessapi.ActorStack, stack); return err }(),
		"space":   func() error { _, err := port.BySpace(ctx, a.ID, dev.ID); return err }(),
		"unbinds": port.UnbindActor(ctx, a.ID, accessapi.ActorStack, stack),
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
