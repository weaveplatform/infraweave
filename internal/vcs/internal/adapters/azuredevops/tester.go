package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// named is how many projects a failed check names before counting the
// rest.
const named = 10

// Test checks that in's token reaches its organization, and that it reads
// the repositories of every project the integration reaches and the
// organization's service hook subscriptions, which a project's webhook is
// one of. A token that cannot read the organization's projects fails both
// checks, with the reason. Whether the token may create a subscription,
// not only read them, shows only when one is created, which a test does
// not do.
func (p *Provider) Test(ctx context.Context, in port.Integration) ([]port.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var first list[struct{}]
	if _, err := p.decode(ctx, in, "/_apis/projects", url.Values{"$top": {"1"}}, "projects",
		&first); err != nil {
		return []port.Check{
			{Type: port.CheckConnection, Reason: err.Error()},
			{Type: port.CheckPermissions, Reason: "The token could not be read."},
		}, nil
	}
	var reasons []string
	switch unread, err := p.unread(ctx, in); {
	case err != nil:
		reasons = append(reasons, "The token's projects could not be listed: "+err.Error()+".")
	case len(unread) > 0:
		reasons = append(reasons, "The token cannot read the repositories of "+names(unread)+
			", so it reads none there.")
	}
	var hooks list[struct{}]
	if _, err := p.decode(ctx, in, "/_apis/hooks/subscriptions", url.Values{}, "service hooks",
		&hooks); err != nil {
		reasons = append(
			reasons,
			"The token cannot read service hooks, so it installs no webhook: "+
				err.Error()+".",
		)
	}
	reason := strings.Join(reasons, " ")
	return []port.Check{
		{Type: port.CheckConnection, Passed: true},
		{Type: port.CheckPermissions, Passed: reason == "", Reason: reason},
	}, nil
}

// unread lists, sorted, the projects in's integration reaches whose
// repositories its token cannot list, an accessible project the
// organization lacks included.
func (p *Provider) unread(ctx context.Context, in port.Integration) ([]string, error) {
	projects, err := p.projects(ctx, in)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, project := range projects {
		_, err := p.repositories(ctx, in, project)
		switch {
		case errors.Is(err, port.ErrNotFound) || errors.Is(err, ErrCredential):
			out = append(out, project)
		case err != nil:
			return nil, err
		}
	}
	return out, nil
}

// names names the first named of projects, counting the rest.
func names(projects []string) string {
	if len(projects) <= named {
		return strings.Join(projects, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(projects[:named], ", "),
		len(projects)-named)
}
