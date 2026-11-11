package bitbucketdc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// Mint mints a repository access token named name that reads repo alone,
// REPO_READ, expiring after the whole days until expires, one at least,
// as Bitbucket counts a token's life in days. A token that does not
// administer repo mints none, port.ErrCannotMint.
func (p *Provider) Mint(
	ctx context.Context,
	repo port.Repository,
	name string,
	expires time.Time,
) (port.Credential, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return port.Credential{}, err
	}
	days := max(1, int(math.Ceil(time.Until(expires).Hours()/24)))
	what := "access token of " + repo.Namespace + "/" + repo.Name
	resp, err := p.send(ctx, *repo.Integration, http.MethodPut, Tokens+rp, nil, map[string]any{
		"name": name, "permissions": []string{"REPO_READ"}, "expiryDays": days,
	}, what)
	if err != nil {
		return port.Credential{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch s := resp.StatusCode; {
	case s == http.StatusForbidden:
		return port.Credential{}, fmt.Errorf(
			"%w: bitbucketdc: %s: the token does not administer it",
			port.ErrCannotMint,
			what,
		)
	case s < http.StatusOK || s >= http.StatusMultipleChoices:
		return port.Credential{}, classify(resp, what)
	}
	var tok struct {
		ID         string `json:"id"`
		Token      string `json:"token"`
		ExpiryDate *int64 `json:"expiryDate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.ID == "" ||
		tok.Token == "" {
		return port.Credential{}, fmt.Errorf("%w: bitbucketdc: %s: no token in the answer",
			kernel.ErrUnavailable, what)
	}
	at := time.Now().Add(time.Duration(days) * 24 * time.Hour).Truncate(time.Second)
	if tok.ExpiryDate != nil {
		at = time.UnixMilli(*tok.ExpiryDate)
	}
	return port.Credential{
		Username: TokenUser, Token: []byte(tok.Token), ID: tok.ID, ExpiresAt: at.UTC(),
	}, nil
}

// Revoke deletes repo's access token id; one Bitbucket no longer has is
// already revoked.
func (p *Provider) Revoke(ctx context.Context, repo port.Repository, id string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	rp, err := repoPath(repo)
	if err != nil {
		return err
	}
	what := "access token " + id + " of " + repo.Namespace + "/" + repo.Name
	resp, err := p.send(ctx, *repo.Integration, http.MethodDelete,
		Tokens+rp+"/"+url.PathEscape(id), nil, nil, what)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if s := resp.StatusCode; s == http.StatusNotFound ||
		s >= http.StatusOK && s < http.StatusMultipleChoices {
		return nil
	}
	return classify(resp, what)
}

// Lend is in's own access token, which git checks out with as the
// username the integration names.
func (p *Provider) Lend(in port.Integration) port.Credential {
	return port.Credential{
		Username: in.Settings.Username, Token: bytes.TrimSpace(bytes.Clone(in.Credential)),
	}
}
