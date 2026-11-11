package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// AzureDevOpsRepoIntegrationCreate creates an Azure DevOps integration
// over a personal access token, reaching the projects it names or, when
// it names none, every project of the organization, with a webhook
// password of its own, which its service hooks send.
func (s Server) AzureDevOpsRepoIntegrationCreate(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationCreateRequestObject,
) (api.AzureDevOpsRepoIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := integration.Input{
		Provider: integration.AzureDevOps,
		Labels:   []string{},
		Settings: port.Settings{
			OrganizationURL: b.OrganizationURL,
			UserFacingHost:  or(b.UserFacingHost, b.OrganizationURL),
		},
		Secrets: &integration.Secrets{
			Credential: []byte(b.PersonalAccessToken), WebhookSecret: []byte(webhookSecret()),
		},
	}
	if b.AccessibleProjects != nil {
		in.Settings.AccessibleProjects = *b.AccessibleProjects
	}
	custom(&in, b.CustomInput)
	out, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.azureDevOps(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegrationCreate201JSONResponse{Body: v}, nil
}

// AzureDevOpsRepoIntegration answers one Azure DevOps integration, with
// its webhook password and URL when the caller administers its space.
func (s Server) AzureDevOpsRepoIntegration(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationRequestObject,
) (api.AzureDevOpsRepoIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(ctx, p, req.AzureDevOpsRepoIntegrationId, integration.AzureDevOps)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.azureDevOps(ctx, p, in, admin)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegration200JSONResponse{Body: v}, nil
}

// AzureDevOpsRepoIntegrationUpdate replaces an Azure DevOps integration's
// organization URL, keeping its user-facing host, projects, token and
// each custom field the request leaves out.
func (s Server) AzureDevOpsRepoIntegrationUpdate(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationUpdateRequestObject,
) (api.AzureDevOpsRepoIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, b := req.AzureDevOpsRepoIntegrationId, req.Body
	cur, _, err := s.svc.Read(ctx, p, id, integration.AzureDevOps)
	if err != nil {
		return nil, integrationError(err)
	}
	in := customUpdate(cur, b.CustomInput)
	in.Settings = cur.Settings
	in.Settings.OrganizationURL = b.OrganizationURL
	in.Settings.UserFacingHost = kept(b.UserFacingHost, in.Settings.UserFacingHost)
	if b.AccessibleProjects != nil {
		in.Settings.AccessibleProjects = *b.AccessibleProjects
	}
	if b.PersonalAccessToken != nil {
		in.Secrets = &integration.Secrets{Credential: []byte(*b.PersonalAccessToken)}
	}
	out, err := s.svc.Update(ctx, p, id, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.azureDevOps(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegrationUpdate200JSONResponse{Body: v}, nil
}

// AzureDevOpsRepoIntegrationDelete deletes an Azure DevOps integration,
// detaching the stacks bound to it, and answers it as it was, without its
// secret.
func (s Server) AzureDevOpsRepoIntegrationDelete(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationDeleteRequestObject,
) (api.AzureDevOpsRepoIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Delete(ctx, p, req.AzureDevOpsRepoIntegrationId, integration.AzureDevOps)
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	return api.AzureDevOpsRepoIntegrationDelete200JSONResponse{
		Body: azureDevOpsView(out, slugs, nil),
	}, nil
}

// AzureDevOpsRepoIntegrationTest answers whether every check of an Azure
// DevOps integration's connection test passes.
func (s Server) AzureDevOpsRepoIntegrationTest(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationTestRequestObject,
) (api.AzureDevOpsRepoIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(ctx, p, req.AzureDevOpsRepoIntegrationId, integration.AzureDevOps)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.AzureDevOpsRepoIntegrationTest200JSONResponse{
		Body: testResult(checks).Status == api.SUCCESS,
	}, nil
}

// azureDevOps is in as the Azure DevOps operations answer it, with its
// webhook password and URL when admin is set.
func (s Server) azureDevOps(
	ctx context.Context,
	p kernel.Principal,
	in integration.Integration,
	admin bool,
) (api.AzureDevOpsRepoIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.AzureDevOpsRepoIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	if !admin {
		return azureDevOpsView(in, slugs, nil), nil
	}
	d, err := s.svc.Details(ctx, in)
	if err != nil {
		return api.AzureDevOpsRepoIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return azureDevOpsView(in, slugs, &d), nil
}

// azureDevOpsView is in as the Azure DevOps operations answer it, its
// space named by its slug in slugs, with d's webhook secret, as its
// password, and URL when d is not nil.
func azureDevOpsView(
	in integration.Integration,
	slugs map[kernel.ULID]string,
	d *integration.Details,
) api.AzureDevOpsRepoIntegration {
	labels, projects := in.Labels, in.Settings.AccessibleProjects
	if labels == nil {
		labels = []string{}
	}
	if projects == nil {
		projects = []string{}
	}
	v := api.AzureDevOpsRepoIntegration{
		AccessibleProjects: projects, Description: in.Description, Id: in.ID.String(),
		IsDefault: in.IsDefault, Kind: api.AzureDevOpsRepoIntegrationKindAzureDevOpsRepoIntegration,
		Labels: labels, Name: in.Name, OrganizationURL: in.Settings.OrganizationURL,
		Space: &api.SpaceRef{Id: slugs[in.Space]}, UseGitCheckout: in.UseGitCheckout,
		UserFacingHost: in.Settings.UserFacingHost,
		VcsChecks:      api.VCSCheck(in.Checks.Mode), VcsCheckOptions: checkOptionsView(in.Checks),
	}
	if d != nil {
		v.WebhookPassword, v.WebhookUrl = d.WebhookSecret, d.WebhookURL
	}
	return v
}
