package githubfake

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// errNoRef is a ref, SHA or path a repository lacks.
var errNoRef = errors.New("githubfake: no such ref")

// Author is the name, e-mail and login every seeded commit carries.
const (
	AuthorName  = "Fake Author"
	AuthorEmail = "author@example.com"
	AuthorLogin = "author"
)

// epoch is the time of a repository's first commit; each later one is a
// minute after the one before, so commits order the same on every run.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Repo is a repository the fake serves, held in memory by go-git, so refs,
// annotated tags, trees and their differences behave as Git's do.
type Repo struct {
	Owner, Name string

	mu      sync.Mutex
	git     *git.Repository
	commits int
}

func newRepo(owner, name string) *Repo {
	r, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		panic(fmt.Sprintf("githubfake: init %s/%s: %v", owner, name, err))
	}
	return &Repo{Owner: owner, Name: name, git: r}
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

// resolve is the commit ref names: a branch, then a tag, peeled when it is
// annotated, then a full SHA, as GitHub's commits route resolves one.
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
	return nil, fmt.Errorf("%w: %s", errNoRef, ref)
}

// isTag reports whether tag names one of the repository's tags.
func (r *Repo) isTag(tag string) (string, bool) {
	t, err := r.git.Reference(plumbing.NewTagReferenceName(tag), true)
	if err != nil {
		return "", false
	}
	return t.Hash().String(), true
}

// file is path's content at ref.
func (r *Repo) file(ref, name string) ([]byte, error) {
	c, err := r.resolve(ref)
	if err != nil {
		return nil, err
	}
	f, err := c.File(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s at %s", errNoRef, name, ref)
	}
	s, err := f.Contents()
	return []byte(s), err //nolint:wrapcheck // go-git names the blob
}

// changed lists, sorted, the paths that differ between the commits base
// and head name.
func (r *Repo) changed(base, head string) ([]string, error) {
	trees := make([]*object.Tree, 0, 2)
	for _, ref := range []string{base, head} {
		c, err := r.resolve(ref)
		if err != nil {
			return nil, err
		}
		t, err := c.Tree()
		if err != nil {
			return nil, fmt.Errorf("githubfake: tree of %s: %w", ref, err)
		}
		trees = append(trees, t)
	}
	changes, err := object.DiffTree(trees[0], trees[1])
	if err != nil {
		return nil, fmt.Errorf("githubfake: diff %s...%s: %w", base, head, err)
	}
	var out []string
	for _, ch := range changes {
		for _, name := range []string{ch.From.Name, ch.To.Name} {
			if name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// branches lists the repository's branches with their tips, sorted by name.
func (r *Repo) branches() ([][2]string, error) {
	it, err := r.git.Branches()
	if err != nil {
		return nil, fmt.Errorf("githubfake: branches: %w", err)
	}
	var out [][2]string
	err = it.ForEach(func(ref *plumbing.Reference) error {
		out = append(out, [2]string{ref.Name().Short(), ref.Hash().String()})
		return nil
	})
	slices.SortFunc(out, func(a, b [2]string) int {
		if a[0] < b[0] {
			return -1
		}
		if a[0] > b[0] {
			return 1
		}
		return 0
	})
	return out, err //nolint:wrapcheck // the callback returns none
}

// must panics on a seeding error: the fake's seeding has no failure a test
// recovers from.
func must(err error) {
	if err != nil {
		panic(fmt.Sprintf("githubfake: %v", err))
	}
}
