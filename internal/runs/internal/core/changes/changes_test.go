package changes_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/changes"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

var errDown = errors.New("down")

// world is one run, the key and bytes of the changes document it
// recorded, if any, and its stack's vendor, each read failing with the
// error a test sets.
type world struct {
	run                  lifecycle.Run
	key                  string
	doc                  []byte
	vendor               string
	getErr, changesErr   error
	openErr, settingsErr error
	opened               []string
}

func (w *world) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run,
	error,
) {
	if w.getErr != nil {
		return lifecycle.Run{}, w.getErr
	}
	if acct != w.run.Account || id != w.run.ID {
		return lifecycle.Run{}, read.ErrNotFound
	}
	return w.run, nil
}

func (w *world) Changes(_ context.Context, run lifecycle.Run) (string, bool, error) {
	if run.ID != w.run.ID {
		panic("another run's changes")
	}
	return w.key, w.key != "", w.changesErr
}

func (w *world) Open(_ context.Context, store objectstore.Name, key string) (io.ReadCloser,
	error,
) {
	if store != objectstore.RunArtifacts {
		panic("the " + string(store) + " store")
	}
	w.opened = append(w.opened, key)
	if w.openErr != nil {
		return nil, w.openErr
	}
	return io.NopCloser(bytes.NewReader(w.doc)), nil
}

func (w *world) Settings(_ context.Context, _ kernel.AccountID, id kernel.ULID) (
	stacksapi.Settings, error,
) {
	if id != w.run.Stack {
		panic("another stack's settings")
	}
	return stacksapi.Settings{Vendor: w.vendor}, w.settingsErr
}

// recorded is the changes document of all.json, the recorded Terraform
// plan of one change of every type, and its changes.
func recorded(t *testing.T) ([]byte, []plan.Change) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "iac", "plan", "testdata",
		"terraform", "all.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var doc bytes.Buffer
	if err := plan.Write(&doc, p.Changes); err != nil {
		t.Fatal(err)
	}
	return doc.Bytes(), p.Changes
}

func newWorld(t *testing.T) (*world, *changes.Service, kernel.Principal) {
	t.Helper()
	doc, _ := recorded(t)
	w := &world{
		run: lifecycle.Run{
			Account: kernel.NewAccountID(), ID: kernel.NewULID(), Stack: kernel.NewULID(),
		},
		key: "acct/run/PLANNING-5/changes.json", doc: doc, vendor: "terraform",
	}
	return w, changes.New(changes.Deps{Store: w, Objects: w, Stacks: w}),
		kernel.Principal{AccountID: w.run.Account}
}

// TestUnitChangesRead answers the recorded plan's changes, in its order,
// for the plan phase or no phase, those of one type when the query names
// it, and none of an apply or drift detection, which open nothing.
func TestUnitChangesRead(t *testing.T) {
	_, all := recorded(t)
	adds := []plan.Change{}
	for _, c := range all {
		if c.Metadata.Type == plan.TypeAdd {
			adds = append(adds, c)
		}
	}
	if len(all) != 10 || len(adds) != 1 {
		t.Fatalf("the recording has %d changes, %d adds", len(all), len(adds))
	}
	for name, c := range map[string]struct {
		q    changes.Query
		want changes.Set
	}{
		"latest": {changes.Query{}, changes.Set{Phase: changes.PhasePlan, Changes: all}},
		"plan": {
			changes.Query{Phase: changes.PhasePlan},
			changes.Set{Phase: changes.PhasePlan, Changes: all},
		},
		"adds": {
			changes.Query{Type: plan.TypeAdd}, changes.Set{Phase: changes.PhasePlan, Changes: adds},
		},
		"forgotten": {
			changes.Query{Type: plan.TypeCreateAfterForgotten},
			changes.Set{Phase: changes.PhasePlan, Changes: []plan.Change{}},
		},
		"apply": {changes.Query{Phase: changes.PhaseApply}, changes.Set{Changes: []plan.Change{}}},
		"drift": {changes.Query{Phase: changes.PhaseDrift}, changes.Set{Changes: []plan.Change{}}},
	} {
		for _, op := range []string{"Entities", "Resources"} {
			w, s, p := newWorld(t)
			read := s.Entities
			if op == "Resources" {
				read = s.Resources
			}
			got, err := read(t.Context(), p, w.run.ID.String(), c.q)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s = %+v, %v, want %+v", op, name, got, err, c.want)
			}
			if len(w.opened) > 1 || (len(w.opened) == 1) != (c.want.Phase != "") {
				t.Errorf("%s %s opened %v", op, name, w.opened)
			}
		}
	}
}

// TestUnitChangesNone answers a run that recorded no changes none, with
// no phase.
func TestUnitChangesNone(t *testing.T) {
	w, s, p := newWorld(t)
	w.key = ""
	got, err := s.Entities(t.Context(), p, w.run.ID.String(), changes.Query{})
	if err != nil || got.Phase != "" || got.Changes == nil || len(got.Changes) != 0 ||
		len(w.opened) != 0 {
		t.Errorf("Entities = %+v, %v; opened %v", got, err, w.opened)
	}
}

// TestUnitChangesVendors reads the resource changes of a Terraform or
// OpenTofu stack's run and refuses another vendor's as invalid, which
// Entities reads.
func TestUnitChangesVendors(t *testing.T) {
	for vendor, ok := range map[string]bool{"terraform": true, "opentofu": true, "pulumi": false} {
		w, s, p := newWorld(t)
		w.vendor = vendor
		_, err := s.Resources(t.Context(), p, w.run.ID.String(), changes.Query{})
		if inv, invalid := errors.AsType[*kernel.Invalid](err); ok != (err == nil) ||
			!ok && !invalid || !ok && inv.Detail == "" {
			t.Errorf("Resources of %s = %v", vendor, err)
		}
		if _, err := s.Entities(t.Context(), p, w.run.ID.String(), changes.Query{}); err != nil {
			t.Errorf("Entities of %s = %v", vendor, err)
		}
	}
}

// TestUnitChangesErrors answers read.ErrNotFound for what is no run ID,
// ErrExpired for a document retention removed, the decoder's refusal of
// a document that is not one, and each read's failure.
func TestUnitChangesErrors(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(*world)
		raw  string
		want error
	}{
		"no ID":     {func(*world) {}, "infra", read.ErrNotFound},
		"other run": {func(*world) {}, kernel.NewULID().String(), read.ErrNotFound},
		"run read":  {func(w *world) { w.getErr = errDown }, "", errDown},
		"key read":  {func(w *world) { w.changesErr = errDown }, "", errDown},
		"expired": {
			func(w *world) { w.openErr = objectstore.ErrNotFound }, "", changes.ErrExpired,
		},
		"open":      {func(w *world) { w.openErr = errDown }, "", errDown},
		"malformed": {func(w *world) { w.doc = []byte(`{"version":1}{}`) }, "", plan.ErrMalformed},
		"settings":  {func(w *world) { w.settingsErr = errDown }, "", errDown},
	} {
		w, s, p := newWorld(t)
		c.set(w)
		raw := c.raw
		if raw == "" {
			raw = w.run.ID.String()
		}
		_, err := s.Resources(t.Context(), p, raw, changes.Query{})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: Resources = %v, want %v", name, err, c.want)
		}
		if name == "settings" {
			continue
		}
		if _, err := s.Entities(t.Context(), p, raw, changes.Query{}); !errors.Is(err, c.want) {
			t.Errorf("%s: Entities = %v, want %v", name, err, c.want)
		}
	}
}
