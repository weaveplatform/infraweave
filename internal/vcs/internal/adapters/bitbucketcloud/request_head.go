package bitbucketcloud

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// ResolveRequestHead expands only the advertised immutable prefix, never a
// branch/tag. The native source UUID fences forks and repository renames.
func (p *Provider) ResolveRequestHead(ctx context.Context, in port.Integration, repo vcsapi.Repository, prefix string) (string, error) {
	if port.ValidCommit(prefix) {
		return prefix, nil
	}
	id, valid := requestRepositoryID(repo.ID)
	if tx.In(ctx) || !valid || !requestSegment(repo.Namespace) || !requestSegment(repo.Name) || !cloudRequestHead(prefix) {
		return "", ErrWebhookPayload
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var commit struct {
		Hash string `json:"hash"`
	}
	path := "/repositories/" + url.PathEscape(repo.Namespace) + "/" + url.PathEscape(id) + "/commit/" + strings.ToLower(prefix)
	if err := p.decode(ctx, in, p.api+path, "immutable request head", &commit); err != nil {
		return "", err
	}
	if !port.ValidCommit(commit.Hash) || !strings.HasPrefix(strings.ToLower(commit.Hash), strings.ToLower(prefix)) {
		return "", fmt.Errorf("%w: bitbucketcloud: request head does not match advertised commit", kernel.ErrUnavailable)
	}
	return commit.Hash, nil
}

func requestRepositoryID(raw string) (string, bool) {
	if len(raw) == 38 && raw[0] == '{' && raw[37] == '}' {
		raw = raw[1:37]
	}
	if len(raw) != 36 {
		return "", false
	}
	id, err := uuid.Parse(raw)
	return "{" + id.String() + "}", err == nil && id != uuid.Nil
}

func requestSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\?#% \t\r\n")
}
