package gitmem

import (
	"errors"
	"slices"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// TestUnitRepo checks a lightweight tag, which a host's repository may
// carry though Tag makes none, refs and paths a repository lacks, the
// order of removals, and a seeding failure.
func TestUnitRepo(t *testing.T) {
	r := New()
	first := r.Commit("main", "first", map[string]string{"a.tf": "a", "b.tf": "b"})
	second := r.Commit("main", "second", map[string]string{"a.tf": "", "b.tf": ""})
	must(r.git.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewTagReferenceName("light"), plumbing.NewHash(first))))
	if c, err := r.Resolve("light"); err != nil || c.Hash.String() != first {
		t.Errorf("Resolve(light) = %v, %v", c, err)
	}
	if sha, msg, c, err := r.TagOf("light"); err != nil || sha != first || msg != "" ||
		c.Hash.String() != first {
		t.Errorf("TagOf(light) = %s, %q, %v, %v", sha, msg, c, err)
	}
	if _, _, _, err := r.TagOf("main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("TagOf(main) = %v", err)
	}
	if _, err := r.File("nope", "a.tf"); !errors.Is(err, ErrNotFound) {
		t.Errorf("File at an unknown ref = %v", err)
	}
	for _, refs := range [][2]string{{"nope", second}, {first, "nope"}} {
		if _, err := r.Changes(t.Context(), refs[0], refs[1]); !errors.Is(err, ErrNotFound) {
			t.Errorf("Changes(%s, %s) = %v", refs[0], refs[1], err)
		}
	}
	got, err := r.Changes(t.Context(), first, second)
	if want := []Change{{From: "a.tf"}, {From: "b.tf"}}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Changes removing two files = %v, %v", got, err)
	}
	defer func() {
		if recover() == nil {
			t.Error("a seeding failure did not panic")
		}
	}()
	r.Tag("light", first, "again")
}
