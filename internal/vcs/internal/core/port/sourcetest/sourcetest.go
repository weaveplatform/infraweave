package sourcetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Repository is what a Source must find in the repository under test.
type Repository struct {
	// Repo is the repository, and Missing one its host lacks.
	Repo, Missing port.Repository
	// Branch is a branch whose head is Head.
	Branch string
	// Tag is an annotated tag of Tagged, a commit.
	Tag, Tagged string
	// Base is a commit before Head that is no ref's tip.
	Base, Head string
	// File is a path at Head holding Content.
	File    string
	Content []byte
	// Changed are the paths that differ between Base and Head, sorted.
	Changed []string
}

// ErrContract is a Source that breaks the contract.
var ErrContract = errors.New("source contract broken")

// Check checks src against the contract every Source keeps on r: a
// branch, a tag and a SHA resolve to their commits, the tag naming
// itself; a file reads at a commit and at a branch; the paths two commits
// differ in list; an unknown ref, SHA, repository and path are
// port.ErrNotFound; and the source URL is not empty.
func Check(ctx context.Context, src port.Source, r Repository) error {
	var errs []error
	fail := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("%w: %s", ErrContract, fmt.Sprintf(format, a...)))
	}
	for _, c := range []struct{ ref, hash, tag string }{
		{r.Branch, r.Head, ""},
		{r.Tag, r.Tagged, r.Tag},
		{r.Base, r.Base, ""},
	} {
		got, err := src.ResolveRef(ctx, r.Repo, c.ref)
		switch {
		case err != nil:
			fail("ResolveRef(%s): %v", c.ref, err)
		case got.Hash != c.hash || got.Tag != c.tag:
			fail(
				"ResolveRef(%s) = %s tag %q, want %s tag %q",
				c.ref,
				got.Hash,
				got.Tag,
				c.hash,
				c.tag,
			)
		case got.Message == "" || got.AuthorName == "" || got.Timestamp.IsZero():
			fail("ResolveRef(%s) = %+v, without its message, author or time", c.ref, got)
		}
	}
	for _, ref := range []string{r.Head, r.Branch} {
		b, err := src.ReadFile(ctx, r.Repo, ref, r.File)
		if err != nil || !bytes.Equal(b, r.Content) {
			fail("ReadFile(%s, %s) = %q, %v; want %q", ref, r.File, b, err, r.Content)
		}
	}
	if got, err := src.AffectedFiles(ctx, r.Repo, r.Base, r.Head); err != nil ||
		!slices.Equal(got, r.Changed) {
		fail("AffectedFiles(%s, %s) = %q, %v; want %q", r.Base, r.Head, got, err, r.Changed)
	}
	for name, err := range map[string]error{
		"an unknown ref": errOf(src.ResolveRef(ctx, r.Repo, "sourcetest-no-such-ref")),
		"an unknown SHA": errOf(
			src.ResolveRef(ctx, r.Repo, "0123456789abcdef0123456789abcdef01234567"),
		),
		"an unknown repository": errOf(src.ResolveRef(ctx, r.Missing, r.Branch)),
		"an unknown path":       errOf(src.ReadFile(ctx, r.Repo, r.Head, "sourcetest/no/such/path")),
		"an unknown base": errOf(
			src.AffectedFiles(ctx, r.Repo, "sourcetest-no-such-ref", r.Head),
		),
	} {
		if !errors.Is(err, port.ErrNotFound) {
			fail("%s = %v, want port.ErrNotFound", name, err)
		}
	}
	if src.SourceURL(r.Repo, r.Head) == "" {
		fail("SourceURL(%s) is empty", r.Head)
	}
	return errors.Join(errs...)
}

// errOf is a call's error, its result dropped.
func errOf[T any](_ T, err error) error { return err }

// Run checks src against r, failing t with every clause it breaks.
func Run(t *testing.T, src port.Source, r Repository) {
	t.Helper()
	if err := Check(t.Context(), src, r); err != nil {
		t.Error(err)
	}
}
