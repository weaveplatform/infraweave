package core

import (
	"context"
	"errors"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

type runtimeStore struct {
	Store
	attachmentsErr, contextsErr, elementsErr error
}

func (s runtimeStore) Attachments(context.Context, kernel.AccountID, []kernel.ULID, bool) (map[kernel.ULID][]Attachment, error) {
	return nil, s.attachmentsErr
}

func (s runtimeStore) ByIDs(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID]Context, error) {
	return nil, s.contextsErr
}

func (s runtimeStore) Elements(context.Context, kernel.AccountID, []Owner) (map[kernel.ULID][]Element, error) {
	return nil, s.elementsErr
}

// TestUnitRuntimeConfigReads checks that empty input needs no store and
// that any failed batch read fails the view instead of showing defaults.
func TestUnitRuntimeConfigReads(t *testing.T) {
	if got, err := New(Deps{}).RuntimeConfig(t.Context(), kernel.NewAccountID(), nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty input = %v, %v", got, err)
	}
	down := errors.New("store unavailable")
	for name, store := range map[string]runtimeStore{
		"attachments": {attachmentsErr: down},
		"contexts":    {contextsErr: down},
		"elements":    {elementsErr: down},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := New(Deps{Store: store}).RuntimeConfig(t.Context(), kernel.NewAccountID(), []kernel.ULID{kernel.NewULID()})
			if !errors.Is(err, down) || got != nil {
				t.Errorf("failed read = %v, %v", got, err)
			}
		})
	}
}

// options admits oidc-federation unless refused, or fails with err.
type options struct {
	refused bool
	err     error
}

func (o options) AllowAccount(_ context.Context, _ kernel.OrganizationID, _ kernel.AccountID, feature string) error {
	if o.refused {
		return &featuresapi.Refusal{Feature: feature}
	}
	return o.err
}

// TestUnitRuntimeConfigFederation checks that the runtime configuration
// lists INFRAWEAVE_OIDC_TOKEN, write-only and computed, only when the
// account has oidc-federation and features are bound, that a stored
// element of that name wins, and that a failed organization read or
// feature check fails the view.
func TestUnitRuntimeConfigFederation(t *testing.T) {
	org := func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
		return kernel.NewOrganizationID(), nil
	}
	stack := kernel.NewULID()
	token := func(d Deps) []stacksapi.ConfigElementWithSource {
		t.Helper()
		d.Store = runtimeStore{}
		got, err := New(d).RuntimeConfig(t.Context(), kernel.NewAccountID(), []kernel.ULID{stack})
		if err != nil {
			t.Fatal(err)
		}
		var out []stacksapi.ConfigElementWithSource
		for _, e := range got[stack] {
			if e.ID == kernel.OIDCTokenVariable {
				out = append(out, e)
			}
		}
		return out
	}
	if got := token(Deps{Features: options{}, Organization: org}); len(got) != 1 || !got[0].WriteOnly ||
		!got[0].Runtime || got[0].Value != nil {
		t.Errorf("federated = %+v", got)
	}
	for name, d := range map[string]Deps{
		"refused": {Features: options{refused: true}, Organization: org},
		"unbound": {},
		"no org":  {Features: options{}},
	} {
		if got := token(d); len(got) != 0 {
			t.Errorf("%s = %+v", name, got)
		}
	}
	stored := runtimeConfig([]Opened{{Element: Element{Name: kernel.OIDCTokenVariable, Value: new("mine")}}}, nil, true)
	if n := len(stored); n != len(kernel.ComputedEnvironmentNames())+1 {
		t.Errorf("stored %d elements: %+v", n, stored)
	}
	for _, e := range stored {
		if e.ID == kernel.OIDCTokenVariable && e.Runtime {
			t.Errorf("a stored %s shows as computed", e.ID)
		}
	}
	down := errors.New("down")
	for name, d := range map[string]Deps{
		"organization": {Features: options{}, Organization: func(context.Context, kernel.AccountID) (kernel.OrganizationID, error) {
			return kernel.OrganizationID{}, down
		}},
		"features": {Features: options{err: down}, Organization: org},
	} {
		d.Store = runtimeStore{}
		if _, err := New(d).RuntimeConfig(t.Context(), kernel.NewAccountID(), []kernel.ULID{stack}); !errors.Is(err, down) {
			t.Errorf("%s down = %v", name, err)
		}
	}
}
