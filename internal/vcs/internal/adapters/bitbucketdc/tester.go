package bitbucketdc

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// named is how many repositories a failed check names before counting
// the rest.
const named = 10

// Test checks that in's access token reaches its Bitbucket as a user, and
// that the user administers every repository it reads: REPO_ADMIN mints
// the repository access token a run reads with and installs the
// repository's webhook. A token that cannot be read fails both checks,
// with the reason.
func (p *Provider) Test(ctx context.Context, in port.Integration) ([]port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var recent struct{}
	if err := p.decode(ctx, in, "/profile/recent/repos", nil, "user", &recent); err != nil {
		return []port.Check{
			{Type: port.CheckConnection, Reason: err.Error()},
			{Type: port.CheckPermissions, Reason: "The token could not be read."},
		}, nil
	}
	reason := ""
	switch lacking, err := p.unadministered(ctx, in); {
	case err != nil:
		reason = "The token's repositories could not be listed: " + err.Error() + "."
	case len(lacking) > 0:
		reason = "The token is not admin of " + list(lacking) +
			", so it mints no run token and installs no webhook there."
	}
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		{Type: port.CheckPermissions, Passed: reason == "", Reason: reason},
	}, nil
}

// unadministered lists, sorted, the repositories in's user reads but does
// not administer, each by its project's key and slug.
func (p *Provider) unadministered(ctx context.Context, in port.Integration) ([]string, error) {
	read, err := p.repositories(ctx, in, "REPO_READ")
	if err != nil {
		return nil, err
	}
	admin, err := p.repositories(ctx, in, "REPO_ADMIN")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range read {
		if !slices.Contains(admin, r) {
			out = append(out, r.Namespace+"/"+r.Name)
		}
	}
	return out, nil
}

// list names the first named of names, counting the rest.
func list(names []string) string {
	if len(names) <= named {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:named], ", "), len(names)-named)
}
