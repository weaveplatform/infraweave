package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-github/v93/github"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

var _ port.RequestSource = (*Provider)(nil)

// ResolveRequest uses the target installation for accessible fork reads. Native
// repository IDs fence each peer before reading its immutable source object.
// The comparison API supplies only the merge base: its file list truncates at
// 300 paths, so complete trees supply the actual changed-file set.
// ref: https://docs.github.com/en/rest/commits/commits#compare-two-commits
func (p *Provider) ResolveRequest(ctx context.Context, target port.Repository, snapshot port.RequestSnapshot) (port.RequestRevision, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	c, h, err := p.repo(ctx, target)
	if err != nil {
		return port.RequestRevision{}, err
	}
	head, err := requestCommit(ctx, c, snapshot.HeadRepository, snapshot.Head)
	if err != nil {
		return port.RequestRevision{}, err
	}
	base, err := requestCommit(ctx, c, snapshot.BaseRepository, snapshot.Base)
	if err != nil {
		return port.RequestRevision{}, err
	}
	comparison, _, err := c.Repositories.CompareCommits(ctx, target.Namespace, target.Name, snapshot.Base, snapshot.Head, &github.ListOptions{PerPage: 1})
	if err != nil {
		return port.RequestRevision{}, classify(err, "native request merge base")
	}
	mergeBase := comparison.GetMergeBaseCommit().GetSHA()
	if !port.ValidCommit(mergeBase) {
		return port.RequestRevision{}, fmt.Errorf("%w: github: native request merge base is absent", kernel.ErrUnavailable)
	}
	diffBase := base
	if !strings.EqualFold(mergeBase, snapshot.Base) {
		diffBase, _, err = c.Repositories.GetCommit(ctx, target.Namespace, target.Name, mergeBase, nil)
		if err != nil {
			return port.RequestRevision{}, classify(err, "native request merge base commit")
		}
	}
	files := make([]map[string]treeFile, 0, 2)
	for index, commit := range []*github.RepositoryCommit{diffBase, head} {
		peer := snapshot.BaseRepository
		if index == 1 {
			peer = snapshot.HeadRepository
		}
		tree := commit.GetCommit().GetTree().GetSHA()
		if tree == "" {
			return port.RequestRevision{}, fmt.Errorf("%w: github: native request commit tree is absent", kernel.ErrUnavailable)
		}
		entries, err := treeFiles(ctx, c, port.Repository{Namespace: peer.Namespace, Name: peer.Name}, tree)
		if err != nil {
			return port.RequestRevision{}, err
		}
		files = append(files, entries)
	}
	return port.RequestRevision{
		Base:  requestCommitFacts(h, snapshot.BaseRepository, base),
		Head:  requestCommitFacts(h, snapshot.HeadRepository, head),
		Files: changedTreePaths(files[0], files[1]),
	}, nil
}

func requestCommit(ctx context.Context, c *github.Client, peer vcsapi.Repository, ref string) (*github.RepositoryCommit, error) {
	if peer.ID == "" || peer.Namespace == "" || peer.Name == "" || !port.ValidCommit(ref) {
		return nil, fmt.Errorf("%w: github: invalid native request peer", kernel.ErrUnavailable)
	}
	r, _, err := c.Repositories.Get(ctx, peer.Namespace, peer.Name)
	if err != nil {
		return nil, classify(err, "native request peer")
	}
	if r.GetID() <= 0 || strconv.FormatInt(r.GetID(), 10) != peer.ID {
		return nil, fmt.Errorf("%w: github: native request peer changed", kernel.ErrUnavailable)
	}
	commit, _, err := c.Repositories.GetCommit(ctx, peer.Namespace, peer.Name, ref, nil)
	if err != nil {
		return nil, classify(err, "native request commit")
	}
	if !strings.EqualFold(commit.GetSHA(), ref) {
		return nil, fmt.Errorf("%w: github: native request commit changed", kernel.ErrUnavailable)
	}
	return commit, nil
}

func requestCommitFacts(h host, peer vcsapi.Repository, commit *github.RepositoryCommit) vcsapi.Commit {
	out := vcsapi.Commit{
		Hash:       commit.GetSHA(),
		URL:        h.web + "/" + peer.Namespace + "/" + peer.Name + "/commit/" + commit.GetSHA(),
		Message:    commit.GetCommit().GetMessage(),
		AuthorName: commit.GetCommit().GetAuthor().GetName(),
		Timestamp:  commit.GetCommit().GetAuthor().GetDate().Time,
	}
	if author := commit.GetAuthor(); author != nil {
		out.AuthorLogin = author.Login
	}
	return out
}
