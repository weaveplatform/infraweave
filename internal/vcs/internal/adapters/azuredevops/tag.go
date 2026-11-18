package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type annotatedTag struct {
	ObjectID     string `json:"objectId"`
	TaggedObject struct {
		ObjectID   string `json:"objectId"`
		ObjectType string `json:"objectType"`
	} `json:"taggedObject"`
}

// ResolveTag follows only immutable object IDs. Service-hook refUpdates can
// name an annotated-tag object; the current refs endpoint would race a moved
// tag. A lightweight tag is already a commit and needs no peeling.
func (p *Provider) ResolveTag(ctx context.Context, repo port.Repository, head string) (vcsapi.Commit, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return vcsapi.Commit{}, err
	}
	in := *repo.Integration
	seen := map[string]bool{}
	for range 16 {
		if !sha.MatchString(head) || seen[head] {
			return vcsapi.Commit{}, fmt.Errorf("%w: invalid immutable tag chain", kernel.ErrUnavailable)
		}
		seen[head] = true
		var tag annotatedTag
		_, err := p.decode(ctx, in, rp+"/annotatedtags/"+url.PathEscape(head), url.Values{}, "immutable tag", &tag)
		if errors.Is(err, port.ErrNotFound) {
			return p.resolve(ctx, repo, head)
		}
		if err != nil {
			return vcsapi.Commit{}, err
		}
		if tag.ObjectID != head || !sha.MatchString(tag.TaggedObject.ObjectID) {
			return vcsapi.Commit{}, fmt.Errorf("%w: invalid immutable tag object", kernel.ErrUnavailable)
		}
		head = tag.TaggedObject.ObjectID
		switch tag.TaggedObject.ObjectType {
		case "commit":
			return p.resolve(ctx, repo, head)
		case "tag":
		default:
			return vcsapi.Commit{}, fmt.Errorf("%w: tag targets a noncommit object", kernel.ErrUnavailable)
		}
	}
	return vcsapi.Commit{}, fmt.Errorf("%w: immutable tag chain is too deep", kernel.ErrUnavailable)
}
