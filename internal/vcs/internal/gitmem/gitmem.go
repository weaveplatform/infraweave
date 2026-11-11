package gitmem

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// ErrNotFound is a ref, SHA or path a repository lacks.
var ErrNotFound = errors.New("gitmem: not found")

// Author is the name and e-mail every seeded commit and tag carries.
const (
	AuthorName  = "Fake Author"
	AuthorEmail = "author@example.com"
)

// epoch is the time of a repository's first commit; each later one is a
// minute after the one before.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Repo is a Git repository in memory. Its methods are safe for concurrent
// use.
type Repo struct {
	mu      sync.Mutex
	git     *git.Repository
	commits int
}

// New returns an empty repository.
func New() *Repo {
	r, err := git.Init(memory.NewStorage(), memfs.New())
	must(err)
	return &Repo{git: r}
}

// signature is the author and committer of the repository's next commit.
func (r *Repo) signature() *object.Signature {
	r.commits++
	return &object.Signature{
		Name:  AuthorName,
		Email: AuthorEmail,
		When:  epoch.Add(time.Duration(r.commits) * time.Minute),
	}
}

// Commit commits files on branch, creating branch from the commit last
// checked out when it does not exist, and returns the commit's SHA. A file
// whose content is empty is removed.
func (r *Repo) Commit(branch, message string, files map[string]string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	wt, err := r.git.Worktree()
	must(err)
	ref := plumbing.NewBranchReferenceName(branch)
	if _, err := r.git.Head(); errors.Is(err, plumbing.ErrReferenceNotFound) {
		must(r.git.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, ref)))
	} else {
		_, missing := r.git.Reference(ref, false)
		must(wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: missing != nil, Force: true}))
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if files[name] == "" {
			_, err := wt.Remove(name)
			must(err)
			continue
		}
		must(wt.Filesystem.MkdirAll(path.Dir(name), 0o755))
		must(util.WriteFile(wt.Filesystem, name, []byte(files[name]), 0o644))
		_, err := wt.Add(name)
		must(err)
	}
	sig := r.signature()
	h, err := wt.Commit(message, &git.CommitOptions{
		Author: sig, Committer: sig, AllowEmptyCommits: true,
	})
	must(err)
	return h.String()
}

// Branch points branch at commit, creating it when it does not exist.
func (r *Repo) Branch(branch, commit string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	must(r.git.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewBranchReferenceName(branch), plumbing.NewHash(commit))))
}

// Tag tags commit with an annotated tag carrying message.
func (r *Repo) Tag(name, commit, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.git.CreateTag(name, plumbing.NewHash(commit), &git.CreateTagOptions{
		Tagger: r.signature(), Message: message,
	})
	must(err)
}

// Resolve is the commit ref names: a branch, then a tag, peeled when it
// is annotated, then a full SHA, as the providers' commit routes resolve
// one.
func (r *Repo) Resolve(ref string) (*object.Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolve(ref)
}

func (r *Repo) resolve(ref string) (*object.Commit, error) {
	if b, err := r.git.Reference(plumbing.NewBranchReferenceName(ref), true); err == nil {
		return r.git.CommitObject(b.Hash()) //nolint:wrapcheck // go-git names the object
	}
	if t, err := r.git.Reference(plumbing.NewTagReferenceName(ref), true); err == nil {
		if tag, err := r.git.TagObject(t.Hash()); err == nil {
			return tag.Commit() //nolint:wrapcheck // as above
		}
		return r.git.CommitObject(t.Hash()) //nolint:wrapcheck // as above
	}
	if plumbing.IsHash(ref) {
		if c, err := r.git.CommitObject(plumbing.NewHash(ref)); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: ref %s", ErrNotFound, ref)
}

// TagOf is tag's object: its SHA and message, and the commit it peels
// to; ErrNotFound when the repository has no such tag.
func (r *Repo) TagOf(tag string) (sha, message string, commit *object.Commit, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.git.Reference(plumbing.NewTagReferenceName(tag), true)
	if err != nil {
		return "", "", nil, fmt.Errorf("%w: tag %s", ErrNotFound, tag)
	}
	if o, err := r.git.TagObject(t.Hash()); err == nil {
		c, err := o.Commit()
		return t.Hash().
				String(),
			strings.TrimSuffix(
				o.Message,
				"\n",
			), c, err //nolint:wrapcheck // go-git names the object
	}
	c, err := r.git.CommitObject(t.Hash())
	return t.Hash().String(), "", c, err //nolint:wrapcheck // as above
}

// File is path's content at ref.
func (r *Repo) File(ref, name string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, err := r.resolve(ref)
	if err != nil {
		return nil, err
	}
	f, err := c.File(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s at %s", ErrNotFound, name, ref)
	}
	s, err := f.Contents()
	return []byte(s), err //nolint:wrapcheck // go-git names the blob
}

// Change is a path that differs between two commits: From is its name in
// the first, empty when it is added, and To its name in the second, empty
// when it is removed. A rename names both.
type Change struct{ From, To string }

// Changes lists the paths that differ between the commits base and head
// name, sorted by their names in head, then in base, renames detected as
// Git detects them.
func (r *Repo) Changes(ctx context.Context, base, head string) ([]Change, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	trees := make([]*object.Tree, 0, 2)
	for _, ref := range []string{base, head} {
		c, err := r.resolve(ref)
		if err != nil {
			return nil, err
		}
		t, err := c.Tree()
		if err != nil {
			return nil, fmt.Errorf("gitmem: tree of %s: %w", ref, err)
		}
		trees = append(trees, t)
	}
	changes, err := object.DiffTreeWithOptions(ctx, trees[0], trees[1],
		object.DefaultDiffTreeOptions)
	if err != nil {
		return nil, fmt.Errorf("gitmem: diff %s...%s: %w", base, head, err)
	}
	out := make([]Change, 0, len(changes))
	for _, ch := range changes {
		out = append(out, Change{From: ch.From.Name, To: ch.To.Name})
	}
	slices.SortFunc(out, func(a, b Change) int {
		if c := strings.Compare(a.To, b.To); c != 0 {
			return c
		}
		return strings.Compare(a.From, b.From)
	})
	return out, nil
}

// Branch is a branch's name and the SHA of its tip.
type Branch struct{ Name, Commit string }

// Branches lists the repository's branches, sorted by name.
func (r *Repo) Branches() ([]Branch, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	it, err := r.git.Branches()
	if err != nil {
		return nil, fmt.Errorf("gitmem: branches: %w", err)
	}
	var out []Branch
	err = it.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, Branch{Name: ref.Name().Short(), Commit: ref.Hash().String()})
		return nil
	})
	slices.SortFunc(out, func(a, b Branch) int { return strings.Compare(a.Name, b.Name) })
	return out, err //nolint:wrapcheck // the callback returns none
}

// must panics on a seeding error: the fakes' seeding has no failure a
// test recovers from.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("gitmem: %v", err))
	}
}
