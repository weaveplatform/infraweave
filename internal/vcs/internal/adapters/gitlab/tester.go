package gitlab

import (
	"context"
	"fmt"
	"slices"
	"strings"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// scope is the token scope infraweave needs: GitLab has none narrower
// that writes commit statuses, merge request comments and environments.
const scope = "api"

// named is how many projects a failed check names before counting the
// rest.
const named = 10

// Test checks that in's token reaches its host, holds the api scope, and
// is Maintainer of every project it reaches, the role that mints a run's
// project access token; a project it is not Maintainer of gives its runs
// the integration's token. A token that cannot be read fails both checks,
// with the reason.
func (p *Provider) Test(ctx context.Context, in port.Integration) ([]port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	failed := func(reason string) []port.Check {
		return []port.Check{
			{Type: port.CheckConnection, Reason: reason},
			{Type: port.CheckPermissions, Reason: "The token could not be read."},
		}
	}
	c, _, err := p.client(in)
	if err != nil {
		return failed(err.Error()), nil
	}
	if _, resp, err := c.Users.CurrentUser(gitlab.WithContext(ctx)); err != nil {
		return failed(classify(resp, err, "user").Error()), nil
	}
	var reasons []string
	switch tok, resp, err := c.PersonalAccessTokens.GetSinglePersonalAccessToken(
		gitlab.WithContext(ctx)); {
	case err != nil:
		reasons = append(reasons, "The token's scopes could not be read: "+
			classify(resp, err, "token").Error()+".")
	case !slices.Contains(tok.Scopes, scope):
		reasons = append(reasons, "The token lacks the "+scope+" scope.")
	}
	lacking, err := unmaintained(ctx, c)
	switch {
	case err != nil:
		reasons = append(reasons, "The token's projects could not be listed: "+err.Error()+".")
	case len(lacking) > 0:
		reasons = append(reasons, "The token is not Maintainer of "+list(lacking)+
			", so it mints no run token there.")
	}
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		{
			Type:   port.CheckPermissions,
			Passed: len(reasons) == 0,
			Reason: strings.Join(reasons, " "),
		},
	}, nil
}

// unmaintained lists, sorted, the projects c's user is a member of below
// Maintainer.
func unmaintained(ctx context.Context, c *gitlab.Client) ([]string, error) {
	member, err := projects(ctx, c, 0)
	if err != nil {
		return nil, err
	}
	maintained, err := projects(ctx, c, gitlab.MaintainerPermissions)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(member, func(p string) bool {
		_, ok := slices.BinarySearch(maintained, p)
		return ok
	}), nil
}

// list names the first named of names, counting the rest.
func list(names []string) string {
	if len(names) <= named {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:named], ", "), len(names)-named)
}
