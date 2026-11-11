package bitbucketcloud

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

// Test checks that in's e-mail and API token reach Bitbucket, and that
// the account is admin of every repository it is a member of, the
// permission that installs the repository's webhook; Bitbucket mints no
// narrower token, so a run reads with the integration's own. A token that
// cannot be read fails both checks, with the reason.
func (p *Provider) Test(ctx context.Context, in port.Integration) ([]port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var user struct{}
	if err := p.decode(ctx, in, p.api+"/user", "user", &user); err != nil {
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
			", so it installs no webhook there."
	}
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		{Type: port.CheckPermissions, Passed: reason == "", Reason: reason},
	}, nil
}

// unadministered lists, sorted, the repositories in's account is a
// member of but not admin of, each by its workspace and slug.
func (p *Provider) unadministered(ctx context.Context, in port.Integration) ([]string, error) {
	member, err := p.repositories(ctx, in, "member")
	if err != nil {
		return nil, err
	}
	admin, err := p.repositories(ctx, in, "admin")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range member {
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
