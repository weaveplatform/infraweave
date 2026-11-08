package role_test

import (
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitSeed(t *testing.T) {
	acct := kernel.NewAccountID()
	got := role.Seed(acct)
	slugs := make([]string, 0, len(got))
	ids := map[kernel.ULID]bool{}
	for _, r := range got {
		if r.AccountID != acct || !r.System || r.Name == "" || r.Description == "" {
			t.Errorf("seeded %+v", r)
		}
		slugs = append(slugs, r.Slug)
		ids[r.ID] = true
	}
	want := []string{
		role.SpaceReader, role.SpaceWriter, role.SpaceAdmin,
		role.WorkerPoolController, role.DriftDetectionManager,
	}
	if !slices.Equal(slugs, want) || len(ids) != len(want) {
		t.Errorf("Seed = %v with %d IDs, want %v with distinct IDs", slugs, len(ids), want)
	}
}

// TestUnitSystemActions checks each system role against decision 0007's
// definition from the fallback column.
func TestUnitSystemActions(t *testing.T) {
	actions := func(slug string) []accessapi.Action {
		t.Helper()
		a, ok := role.SystemActions(slug)
		if !ok {
			t.Fatalf("%s is not a system role", slug)
		}
		return a
	}
	reader, writer, admin := actions(
		role.SpaceReader,
	), actions(
		role.SpaceWriter,
	), actions(
		role.SpaceAdmin,
	)
	for _, a := range accessapi.Actions() {
		in := func(set []accessapi.Action) bool { return slices.Contains(set, a.Action) }
		if in(admin) != a.Assignable() {
			t.Errorf("space-admin holds %s: %t, assignable %t", a.Action, in(admin), a.Assignable())
		}
		if want := a.Assignable() && a.Fallback == accessapi.FallbackReader; in(reader) != want {
			t.Errorf("space-reader holds %s: %t, want %t", a.Action, in(reader), want)
		}
		if want := a.Assignable() && a.Fallback != accessapi.FallbackAdmin; in(writer) != want {
			t.Errorf("space-writer holds %s: %t, want %t", a.Action, in(writer), want)
		}
	}
	if len(reader) == 0 || len(writer) <= len(reader) || len(admin) != 130 {
		t.Errorf("reader %d, writer %d, admin %d actions; want admin to hold the 130 assignable",
			len(reader), len(writer), len(admin))
	}
	for slug, want := range map[string][]accessapi.Action{
		role.WorkerPoolController: {
			accessapi.ActionWorkerPoolCreate, accessapi.ActionWorkerPoolDelete,
			accessapi.ActionWorkerPoolUpdate, accessapi.ActionSpaceRead,
		},
		role.DriftDetectionManager: {
			accessapi.ActionDriftDetectionIntegrationCreate, accessapi.ActionDriftDetectionIntegrationUpdate,
			accessapi.ActionDriftDetectionIntegrationDelete, accessapi.ActionSpaceRead,
		},
	} {
		got := actions(slug)
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", slug, got, want)
		}
		for _, a := range want {
			if !slices.Contains(got, a) {
				t.Errorf("%s lacks %s", slug, a)
			}
		}
	}
	if _, ok := role.SystemActions("custom"); ok {
		t.Error("a custom slug has system actions")
	}
}
