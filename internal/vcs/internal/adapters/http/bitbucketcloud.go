package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// BitbucketCloudIntegrationCreateV2 creates a Bitbucket Cloud integration
// over an Atlassian account's email and API token, with a webhook secret
// of its own, which its repositories' webhooks sign their deliveries with.
// It always checks out with git: Bitbucket Cloud serves no archive to an
// API token.
func (s Server) BitbucketCloudIntegrationCreateV2(
	ctx context.Context,
	req api.BitbucketCloudIntegrationCreateV2RequestObject,
) (api.BitbucketCloudIntegrationCreateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := integration.Input{
		Provider: integration.BitbucketCloud,
		Labels:   []string{},
		Settings: port.Settings{Email: b.Email},
		Secrets: &integration.Secrets{
			Credential: []byte(b.ApiToken), WebhookSecret: []byte(webhookSecret()),
		},
	}
	custom(&in, b.CustomInput)
	in.UseGitCheckout = true
	out, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketCloud(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegrationCreateV2201JSONResponse{Body: v}, nil
}

// BitbucketCloudIntegration answers one Bitbucket Cloud integration, with
// its webhook secret and URL when the caller administers its space.
func (s Server) BitbucketCloudIntegration(
	ctx context.Context,
	req api.BitbucketCloudIntegrationRequestObject,
) (api.BitbucketCloudIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(
		ctx,
		p,
		req.BitbucketCloudIntegrationId,
		integration.BitbucketCloud,
	)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketCloud(ctx, p, in, admin)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegration200JSONResponse{Body: v}, nil
}

// BitbucketCloudIntegrationUpdateV2 replaces a Bitbucket Cloud
// integration's email, and its API token when the request carries one,
// keeping each custom field the request leaves out.
func (s Server) BitbucketCloudIntegrationUpdateV2(
	ctx context.Context,
	req api.BitbucketCloudIntegrationUpdateV2RequestObject,
) (api.BitbucketCloudIntegrationUpdateV2ResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, b := req.BitbucketCloudIntegrationId, req.Body
	cur, _, err := s.svc.Read(ctx, p, id, integration.BitbucketCloud)
	if err != nil {
		return nil, integrationError(err)
	}
	in := customUpdate(cur, b.CustomInput)
	in.Settings = cur.Settings
	in.Settings.Email = b.Email
	in.UseGitCheckout = true
	if b.ApiToken != nil {
		in.Secrets = &integration.Secrets{Credential: []byte(*b.ApiToken)}
	}
	out, err := s.svc.Update(ctx, p, id, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketCloud(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegrationUpdateV2200JSONResponse{Body: v}, nil
}

// BitbucketCloudIntegrationDelete deletes a Bitbucket Cloud integration,
// detaching the stacks bound to it, and answers it as it was, without its
// secret.
func (s Server) BitbucketCloudIntegrationDelete(
	ctx context.Context,
	req api.BitbucketCloudIntegrationDeleteRequestObject,
) (api.BitbucketCloudIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Delete(ctx, p, req.BitbucketCloudIntegrationId, integration.BitbucketCloud)
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	return api.BitbucketCloudIntegrationDelete200JSONResponse{
		Body: bitbucketCloudView(out, slugs, nil),
	}, nil
}

// BitbucketCloudIntegrationTest answers whether every check of a
// Bitbucket Cloud integration's connection test passes.
func (s Server) BitbucketCloudIntegrationTest(
	ctx context.Context,
	req api.BitbucketCloudIntegrationTestRequestObject,
) (api.BitbucketCloudIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(ctx, p, req.BitbucketCloudIntegrationId, integration.BitbucketCloud)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.BitbucketCloudIntegrationTest200JSONResponse{
		Body: testResult(checks).Status == api.SUCCESS,
	}, nil
}

// bitbucketCloud is in as the Bitbucket Cloud operations answer it, with
// its webhook secret and URL when admin is set.
func (s Server) bitbucketCloud(
	ctx context.Context,
	p kernel.Principal,
	in integration.Integration,
	admin bool,
) (api.BitbucketCloudIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.BitbucketCloudIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	if !admin {
		return bitbucketCloudView(in, slugs, nil), nil
	}
	d, err := s.svc.Details(ctx, in)
	if err != nil {
		return api.BitbucketCloudIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return bitbucketCloudView(in, slugs, &d), nil
}

// bitbucketCloudView is in as the Bitbucket Cloud operations answer it,
// its space named by its slug in slugs, with d's webhook secret and URL
// when d is not nil. Its username is empty: an API token authenticates
// with the account's email, which Bitbucket Cloud's API and git both take.
func bitbucketCloudView(
	in integration.Integration,
	slugs map[kernel.ULID]string,
	d *integration.Details,
) api.BitbucketCloudIntegration {
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	v := api.BitbucketCloudIntegration{
		Description: in.Description, Email: in.Settings.Email, Id: in.ID.String(),
		IsDefault: in.IsDefault, Kind: api.BitbucketCloudIntegrationKindBitbucketCloudIntegration,
		Labels: labels, Name: in.Name, Space: &api.SpaceRef{Id: slugs[in.Space]},
		UseGitCheckout: true, Username: in.Settings.Username,
		VcsChecks: api.VCSCheck(in.Checks.Mode), VcsCheckOptions: checkOptionsView(in.Checks),
	}
	if d != nil {
		v.WebhookSecret, v.WebhookUrl = d.WebhookSecret, d.WebhookURL
	}
	return v
}
