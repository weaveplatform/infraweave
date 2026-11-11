package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// GitlabIntegrationCreate creates a GitLab integration over an access
// token, with a webhook secret of its own, which GitLab's project hooks
// send as their token.
func (s Server) GitlabIntegrationCreate(
	ctx context.Context,
	req api.GitlabIntegrationCreateRequestObject,
) (api.GitlabIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := integration.Input{
		Provider: integration.GitLab,
		Labels:   []string{},
		Settings: port.Settings{
			APIHost:        b.ApiHost,
			UserFacingHost: or(b.UserFacingHost, b.ApiHost),
		},
		Secrets: &integration.Secrets{
			Credential: []byte(b.PrivateToken), WebhookSecret: []byte(webhookSecret()),
		},
	}
	custom(&in, b.CustomInput)
	out, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.gitlab(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegrationCreate201JSONResponse{Body: v}, nil
}

// GitlabIntegration answers one GitLab integration, with its webhook
// secret and URL when the caller administers its space.
func (s Server) GitlabIntegration(
	ctx context.Context,
	req api.GitlabIntegrationRequestObject,
) (api.GitlabIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(ctx, p, req.GitlabIntegrationId, integration.GitLab)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.gitlab(ctx, p, in, admin)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegration200JSONResponse{Body: v}, nil
}

// GitlabIntegrationUpdate replaces a GitLab integration's settings,
// keeping its hosts, token and each custom field the request leaves out.
func (s Server) GitlabIntegrationUpdate(
	ctx context.Context,
	req api.GitlabIntegrationUpdateRequestObject,
) (api.GitlabIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, b := req.GitlabIntegrationId, req.Body
	cur, _, err := s.svc.Read(ctx, p, id, integration.GitLab)
	if err != nil {
		return nil, integrationError(err)
	}
	in := customUpdate(cur, b.CustomInput)
	in.Settings = cur.Settings
	if b.ApiHost != nil {
		in.Settings.APIHost = *b.ApiHost
	}
	in.Settings.UserFacingHost = kept(b.UserFacingHost, in.Settings.UserFacingHost)
	if b.PrivateToken != nil {
		in.Secrets = &integration.Secrets{Credential: []byte(*b.PrivateToken)}
	}
	out, err := s.svc.Update(ctx, p, id, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.gitlab(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegrationUpdate200JSONResponse{Body: v}, nil
}

// GitlabIntegrationDelete deletes a GitLab integration, detaching the
// stacks bound to it, and answers it as it was, without its secret.
func (s Server) GitlabIntegrationDelete(
	ctx context.Context,
	req api.GitlabIntegrationDeleteRequestObject,
) (api.GitlabIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Delete(ctx, p, req.GitlabIntegrationId, integration.GitLab)
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	return api.GitlabIntegrationDelete200JSONResponse{Body: gitlabView(out, slugs, nil)}, nil
}

// GitlabIntegrationTest answers whether every check of a GitLab
// integration's connection test passes.
func (s Server) GitlabIntegrationTest(
	ctx context.Context,
	req api.GitlabIntegrationTestRequestObject,
) (api.GitlabIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(ctx, p, req.GitlabIntegrationId, integration.GitLab)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.GitlabIntegrationTest200JSONResponse{
		Body: testResult(checks).Status == api.SUCCESS,
	}, nil
}

// gitlab is in as the GitLab operations answer it, with its webhook
// secret and URL when admin is set.
func (s Server) gitlab(
	ctx context.Context,
	p kernel.Principal,
	in integration.Integration,
	admin bool,
) (api.GitlabIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.GitlabIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	if !admin {
		return gitlabView(in, slugs, nil), nil
	}
	d, err := s.svc.Details(ctx, in)
	if err != nil {
		return api.GitlabIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return gitlabView(in, slugs, &d), nil
}

// gitlabView is in as the GitLab operations answer it, its space named by
// its slug in slugs, with d's webhook secret and URL when d is not nil.
func gitlabView(
	in integration.Integration,
	slugs map[kernel.ULID]string,
	d *integration.Details,
) api.GitlabIntegration {
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	v := api.GitlabIntegration{
		ApiHost: in.Settings.APIHost, Description: in.Description, Id: in.ID.String(),
		IsDefault: in.IsDefault, Kind: api.GitlabIntegrationKindGitlabIntegration,
		Labels: labels, Name: in.Name, Space: &api.SpaceRef{Id: slugs[in.Space]},
		UseGitCheckout: in.UseGitCheckout, UserFacingHost: in.Settings.UserFacingHost,
		VcsChecks: api.VCSCheck(in.Checks.Mode), VcsCheckOptions: checkOptionsView(in.Checks),
	}
	if d != nil {
		v.WebhookSecret, v.WebhookUrl = d.WebhookSecret, d.WebhookURL
	}
	return v
}
