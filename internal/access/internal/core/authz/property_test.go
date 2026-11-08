package authz_test

import (
	"math/rand/v2"
	"slices"
	"testing"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitClosureProperty is the evaluator half of the authorization
// property test: on random trees, after random moves and with random
// bindings, inherit flags and principals, Reachable(action) is exactly
// the spaces where Authorize allows the action, for an action of each
// class.
//
// ref: decision 0007 § "Verification"
func TestUnitClosureProperty(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11)) //nolint:gosec // a reproducible test sequence
	actions := []accessapi.Action{
		accessapi.ActionSpaceRead,          // reader
		accessapi.ActionStackLock,          // writer
		accessapi.ActionStackCreate,        // admin
		accessapi.ActionWorkerPoolCycle,    // the custom role's
		accessapi.ActionStackWebhookCreate, // disabled: admins only
		accessapi.ActionStackStateRead,     // refused to runs
		accessapi.ActionAccountAdmin,
		accessapi.ActionAuthenticated,
		accessapi.ActionOrganizationAdmin,
	}
	slugs := []string{
		role.SpaceReader, role.SpaceWriter, role.SpaceAdmin, role.WorkerPoolController, "custom",
	}
	partial := 0
	for trial := range 300 {
		x := newAccount()
		names := make([]string, 0, 14)
		names = append(names, "top", "a", "a1", "a11", "a111", "legacy")
		parent := map[string]string{
			"a":      "top",
			"a1":     "a",
			"a11":    "a1",
			"a111":   "a11",
			"legacy": "top",
		}
		// Grow the tree, then move random spaces below others outside
		// their own subtree.
		for i := range r.IntN(8) {
			name := string(rune('p' + i))
			parent[name] = names[r.IntN(len(names))]
			names = append(names, name)
		}
		within := func(s, a string) bool {
			for x := s; x != ""; x = parent[x] {
				if x == a {
					return true
				}
			}
			return false
		}
		for range r.IntN(4) {
			s, p := names[1+r.IntN(len(names)-1)], names[r.IntN(len(names))]
			if !within(p, s) {
				parent[s] = p
			}
		}
		for _, n := range names {
			if _, ok := x.spaces[n]; !ok {
				x.spaces[n] = kernel.NewULID()
			}
		}
		x.data.Spaces = nil
		for _, n := range names {
			x.data.Spaces = append(x.data.Spaces, authz.Space{
				ID: x.spaces[n], Parent: x.spaces[parent[n]], Inherit: r.IntN(2) == 0,
			})
		}
		m, key, stack := kernel.NewMemberID(), kernel.NewULID(), kernel.NewULID()
		team := x.group("team")
		actors := []struct {
			kind role.ActorKind
			id   kernel.ULID
		}{{role.ActorUser, m.ULID}, {role.ActorGroup, team}, {role.ActorAPIKey, key}, {role.ActorStack, stack}}
		for range r.IntN(6) {
			a := actors[r.IntN(len(actors))]
			x.bind(a.kind, a.id, slugs[r.IntN(len(slugs))], names[r.IntN(len(names))])
		}
		snap := authz.Build(x.data)
		whos := []authz.Who{
			x.user(m, "team"),
			{Account: x.id, Kind: kernel.PrincipalAPIKey, Actor: key},
			{
				Account: x.id, Kind: kernel.PrincipalRun, Stack: stack,
				StackHome: x.spaces[names[r.IntN(len(names))]],
			},
		}
		for _, w := range whos {
			for _, a := range actions {
				var want []kernel.ULID
				for _, n := range names {
					if snap.Authorize(w, a, x.spaces[n]) {
						want = append(want, x.spaces[n])
					}
				}
				got := snap.Reachable(w, a)
				slices.SortFunc(got, compare)
				slices.SortFunc(want, compare)
				if len(got) > 0 && len(got) < len(names) {
					partial++
				}
				if !slices.Equal(got, want) {
					t.Fatalf("trial %d, %s as %s: Reachable = %d spaces, Authorize allows %d",
						trial, a, w.Kind, len(got), len(want))
				}
			}
		}
	}
	// Most trials must reach some spaces and not others, or the property
	// holds of nothing.
	if partial < 300 {
		t.Errorf("only %d decisions reached part of their tree", partial)
	}
}

func compare(a, b kernel.ULID) int {
	for i := range a {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return 0
}
