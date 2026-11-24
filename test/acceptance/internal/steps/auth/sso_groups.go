package auth

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// registerGroups registers the steps that give a provider's sign-ins
// groups and admit the people who sign in to the home organization's
// accounts, so that the groups reach each account's managed user groups.
func (s *ssoSteps) registerGroups(sc *godog.ScenarioContext) {
	sc.Step(`^the member "(\w+)" is invited to the (home|home and sibling) accounts?$`, s.invited)
	sc.Step(`^the provider "(\w+)" takes the groups from the claim "([^"]+)"$`, s.groupsClaim)
}

// invited seeds a pending member of the home organization under login,
// admitted to each named account as an invitation admits it, with no
// groups yet, and names its ID {login} for the scenario's requests. Its
// first sign-in, which no identity binds yet, makes it active.
func (s *ssoSteps) invited(ctx context.Context, login, accounts string) error {
	id := kernel.NewMemberID()
	tenants := []harness.Tenant{harness.Home}
	if accounts == "home and sibling" {
		tenants = append(tenants, harness.Sibling)
	}
	if _, err := s.w.Query(ctx, fmt.Sprintf(`INSERT INTO members (organization_id, id, login, name, email, status)
		VALUES ('%s', '%s', %s, %s, '', 'pending')`,
		harness.UUIDOf(harness.Home.Org), id.UUID(), harness.Literal(login), harness.Literal(login))); err != nil {
		return err //nolint:wrapcheck // names the query
	}
	for _, t := range tenants {
		if _, err := s.w.Query(ctx, fmt.Sprintf(`INSERT INTO account_users (account_id, organization_id, member_id)
			VALUES ('%s', '%s', '%s')`, harness.UUIDOf(t.Account), harness.UUIDOf(harness.Home.Org), id.UUID())); err != nil {
			return err //nolint:wrapcheck // names the query
		}
	}
	s.w.IDs[login] = id.String()
	return nil
}

// groupsClaim maps the provider's groups to claim, as an organization
// admin's claim mapping does, keeping the mapping's other entries.
func (s *ssoSteps) groupsClaim(ctx context.Context, name, claim string) error {
	p, err := s.named(name)
	if err != nil {
		return err
	}
	_, err = s.w.Query(ctx, fmt.Sprintf(`UPDATE identity_providers SET settings = jsonb_set(settings,
		'{claimMapping}', coalesce(settings->'claimMapping', '[]'::jsonb) ||
			jsonb_build_array(jsonb_build_object('name', 'groups', 'value', %s)))
		WHERE id = '%s'`, harness.Literal(claim), harness.UUIDOf(p.id)))
	return err //nolint:wrapcheck // names the query
}
