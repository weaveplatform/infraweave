package gitmem

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Range returns every commit reachable from head and not from base, newest
// first. An empty base asks for the complete head history. Provider fakes page
// these actual Git objects independently of their file-diff responses.
func (r *Repo) Range(ctx context.Context, base, head string) ([]*object.Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	excluded := map[plumbing.Hash]*object.Commit{}
	if base != "" {
		if err := r.history(ctx, base, excluded); err != nil {
			return nil, err
		}
	}
	commits := map[plumbing.Hash]*object.Commit{}
	if err := r.history(ctx, head, commits); err != nil {
		return nil, err
	}
	out := make([]*object.Commit, 0, len(commits))
	for hash, commit := range commits {
		if _, exists := excluded[hash]; !exists {
			out = append(out, commit)
		}
	}
	slices.SortFunc(out, func(a, b *object.Commit) int {
		if order := b.Committer.When.Compare(a.Committer.When); order != 0 {
			return order
		}
		return strings.Compare(a.Hash.String(), b.Hash.String())
	})
	return out, nil
}

func (r *Repo) history(ctx context.Context, ref string, commits map[plumbing.Hash]*object.Commit) error {
	first, err := r.resolve(ref)
	if err != nil {
		return err
	}
	work := []plumbing.Hash{first.Hash}
	for len(work) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("gitmem: history: %w", err)
		}
		hash := work[len(work)-1]
		work = work[:len(work)-1]
		if _, exists := commits[hash]; exists {
			continue
		}
		commit, err := r.git.CommitObject(hash)
		if err != nil {
			return fmt.Errorf("gitmem: history commit: %w", err)
		}
		commits[hash] = commit
		work = append(work, commit.ParentHashes...)
	}
	return nil
}

// FullTree expands every directory, retaining native object types and modes.
func (r *Repo) FullTree(ctx context.Context, ref string) ([]TreeEntry, error) {
	type directory struct{ ref, prefix string }
	work := []directory{{ref: ref}}
	out := []TreeEntry{}
	for len(work) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("gitmem: full tree: %w", err)
		}
		item := work[len(work)-1]
		work = work[:len(work)-1]
		_, entries, err := r.Tree(item.ref)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			entry.Path = item.prefix + entry.Path
			out = append(out, entry)
			if entry.Type == "tree" {
				work = append(work, directory{entry.SHA, entry.Path + "/"})
			}
		}
	}
	slices.SortFunc(out, func(a, b TreeEntry) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// Orphan creates an independent root with treeRef's exact tree, for native
// forced-push fixtures that cannot rely on a common ancestor.
func (r *Repo) Orphan(branch, treeRef, message string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	base, err := r.resolve(treeRef)
	must(err)
	signature := r.signature()
	commit := &object.Commit{Author: *signature, Committer: *signature, Message: message, TreeHash: base.TreeHash}
	encoded := r.git.Storer.NewEncodedObject()
	must(commit.Encode(encoded))
	hash, err := r.git.Storer.SetEncodedObject(encoded)
	must(err)
	must(r.git.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(branch), hash)))
	return hash.String()
}
