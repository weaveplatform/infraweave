package pg_test

import (
	"errors"
	"strings"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	"github.com/weaveplatform/infraweave/internal/platform/rego"
	"github.com/weaveplatform/infraweave/internal/policies/internal/core"
)

func TestAccPolicyDomainFailuresDoNotCommit(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Stable")
	in := core.Input{Name: "Changed", Body: p.Body, Type: p.Type, Space: "team"}
	write := map[string]func() error{
		"create": func() error { _, err := f.svc.Create(t.Context(), f.principal, in); return err },
		"update": func() error {
			_, err := f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, in)
			return err
		},
		"delete": func() error {
			_, err := f.svc.Delete(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, true)
			return err
		},
	}
	down := errors.New("injected domain port failure")
	f.authz.err = down
	for name, call := range write {
		if err := call(); !errors.Is(err, down) {
			t.Errorf("%s lost authorization failure: %v", name, err)
		}
	}
	for _, space := range []*string{nil, new("team")} {
		if _, _, err := f.svc.Search(t.Context(), f.principal, search.Input{}, space); !errors.Is(err, down) {
			t.Errorf("search lost authorization failure: %v", err)
		}
		if _, err := f.svc.Suggest(t.Context(), f.principal, search.SuggestionsInput{}, space); !errors.Is(err, down) {
			t.Errorf("suggest lost authorization failure: %v", err)
		}
	}
	f.authz.err = nil
	f.authz.denied = true
	for name, call := range write {
		var denied *core.Denied
		if err := call(); !errors.As(err, &denied) || denied.Error() == "" {
			t.Errorf("%s did not refuse denied write: %v", name, err)
		}
	}
	f.authz.denied = false
	f.spaces.err = down
	for _, name := range []string{"create", "update"} {
		if err := write[name](); !errors.Is(err, down) {
			t.Errorf("%s lost space lookup failure: %v", name, err)
		}
	}
	if _, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "No root hold", Body: p.Body, Type: p.Type}); !errors.Is(err, down) {
		t.Errorf("root hold lost failure: %v", err)
	}
	for _, space := range []*string{nil, new("team")} {
		if _, _, err := f.svc.Search(t.Context(), f.principal, search.Input{}, space); !errors.Is(err, down) {
			t.Errorf("search lost space tree failure: %v", err)
		}
		if _, err := f.svc.Suggest(t.Context(), f.principal, search.SuggestionsInput{}, space); !errors.Is(err, down) {
			t.Errorf("suggest lost space tree failure: %v", err)
		}
	}
	f.spaces.err = accessapi.ErrNoSpace
	for _, space := range []string{"", "gone"} {
		_, err := f.svc.Create(t.Context(), f.principal, core.Input{Name: "Gone", Space: space, Body: p.Body, Type: p.Type})
		var missing *core.Missing
		if !errors.As(err, &missing) || missing.Kind != "space" {
			t.Errorf("missing space not concealed: %v", err)
		}
	}
	f.spaces.err = nil
	stored, err := f.store.Get(t.Context(), p.Account, p.ID, false)
	if err != nil || stored.Name != p.Name || stored.Version != p.Version || f.count(t, "policies") != 1 || f.count(t, "events") != 1 {
		t.Fatalf("domain failure partially committed: %+v %v", stored, err)
	}
}

func TestAccPolicyInputValidationPrecedesStorage(t *testing.T) {
	f := newFixture(t)
	p := f.create(t, "Stable")
	for _, in := range []core.Input{
		{Name: "  ", Body: p.Body, Type: p.Type},
		{Name: "Bad labels", Body: p.Body, Type: p.Type, Labels: []string{"same", "same"}},
		{Name: "Oversized", Body: strings.Repeat("#", int(rego.Defaults().MaxBodyBytes)+1), Type: p.Type},
	} {
		for _, update := range []bool{false, true} {
			var err error
			if update {
				_, err = f.svc.Update(t.Context(), f.principal, p.ID.String(), kernel.Expect{}, in)
			} else {
				_, err = f.svc.Create(t.Context(), f.principal, in)
			}
			var invalid *kernel.Invalid
			if !errors.As(err, &invalid) || len(invalid.Errors) == 0 {
				t.Fatalf("invalid input accepted: %s update=%v %v", in.Name, update, err)
			}
		}
	}
	if f.count(t, "policies") != 1 || f.count(t, "events") != 1 {
		t.Fatal("invalid save committed rows")
	}
}
