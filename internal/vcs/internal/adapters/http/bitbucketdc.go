package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// BitbucketDatacenterIntegrationCreate creates a Bitbucket Data Center
// integration over an HTTP access token, with a webhook secret of its
// own, which its repositories' webhooks sign their deliveries with. Its
// username is the one git checks out as.
func (s Server) BitbucketDatacenterIntegrationCreate(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationCreateRequestObject,
) (api.BitbucketDatacenterIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := integration.Input{
		Provider: integration.BitbucketDC,
		Labels:   []string{},
		Settings: port.Settings{
			APIHost: b.ApiHost, UserFacingHost: or(&b.UserFacingHost, b.ApiHost),
			Username: or(b.Username, ""),
		},
		Secrets: &integration.Secrets{
			Credential: []byte(b.AccessToken), WebhookSecret: []byte(webhookSecret()),
		},
	}
	custom(&in, b.CustomInput)
	out, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketDC(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegrationCreate201JSONResponse{Body: v}, nil
}

// BitbucketDatacenterIntegration answers one Bitbucket Data Center
// integration, with its webhook secret and URL when the caller
// administers its space.
func (s Server) BitbucketDatacenterIntegration(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationRequestObject,
) (api.BitbucketDatacenterIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(
		ctx,
		p,
		req.BitbucketDatacenterIntegrationId,
		integration.BitbucketDC,
	)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketDC(ctx, p, in, admin)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegration200JSONResponse{Body: v}, nil
}

// BitbucketDatacenterIntegrationUpdate replaces a Bitbucket Data Center
// integration's hosts, keeping its username, access token and each
// custom field the request leaves out.
func (s Server) BitbucketDatacenterIntegrationUpdate(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationUpdateRequestObject,
) (api.BitbucketDatacenterIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, b := req.BitbucketDatacenterIntegrationId, req.Body
	cur, _, err := s.svc.Read(ctx, p, id, integration.BitbucketDC)
	if err != nil {
		return nil, integrationError(err)
	}
	in := customUpdate(cur, b.CustomInput)
	in.Settings = cur.Settings
	in.Settings.APIHost = b.ApiHost
	in.Settings.UserFacingHost = or(&b.UserFacingHost, b.ApiHost)
	in.Settings.Username = kept(b.Username, in.Settings.Username)
	if b.AccessToken != nil {
		in.Secrets = &integration.Secrets{Credential: []byte(*b.AccessToken)}
	}
	out, err := s.svc.Update(ctx, p, id, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.bitbucketDC(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegrationUpdate200JSONResponse{Body: v}, nil
}

// BitbucketDatacenterIntegrationDelete deletes a Bitbucket Data Center
// integration, detaching the stacks bound to it, and answers it as it
// was, without its secret.
func (s Server) BitbucketDatacenterIntegrationDelete(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationDeleteRequestObject,
) (api.BitbucketDatacenterIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Delete(ctx, p, req.BitbucketDatacenterIntegrationId, integration.BitbucketDC)
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	return api.BitbucketDatacenterIntegrationDelete200JSONResponse{
		Body: bitbucketDCView(out, slugs, nil),
	}, nil
}

// BitbucketDatacenterIntegrationTest answers whether every check of a
// Bitbucket Data Center integration's connection test passes.
func (s Server) BitbucketDatacenterIntegrationTest(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationTestRequestObject,
) (api.BitbucketDatacenterIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(ctx, p, req.BitbucketDatacenterIntegrationId, integration.BitbucketDC)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.BitbucketDatacenterIntegrationTest200JSONResponse{
		Body: testResult(checks).Status == api.SUCCESS,
	}, nil
}

// bitbucketDC is in as the Bitbucket Data Center operations answer it,
// with its webhook secret and URL when admin is set.
func (s Server) bitbucketDC(
	ctx context.Context,
	p kernel.Principal,
	in integration.Integration,
	admin bool,
) (api.BitbucketDatacenterIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.BitbucketDatacenterIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	if !admin {
		return bitbucketDCView(in, slugs, nil), nil
	}
	d, err := s.svc.Details(ctx, in)
	if err != nil {
		return api.BitbucketDatacenterIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return bitbucketDCView(in, slugs, &d), nil
}

// bitbucketDCView is in as the Bitbucket Data Center operations answer
// it, its space named by its slug in slugs, with d's webhook secret and
// URL when d is not nil.
func bitbucketDCView(
	in integration.Integration,
	slugs map[kernel.ULID]string,
	d *integration.Details,
) api.BitbucketDatacenterIntegration {
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	v := api.BitbucketDatacenterIntegration{
		ApiHost:         in.Settings.APIHost,
		Description:     in.Description,
		Id:              in.ID.String(),
		IsDefault:       in.IsDefault,
		Kind:            api.BitbucketDatacenterIntegrationKindBitbucketDatacenterIntegration,
		Labels:          labels,
		Name:            in.Name,
		Space:           &api.SpaceRef{Id: slugs[in.Space]},
		UseGitCheckout:  in.UseGitCheckout,
		UserFacingHost:  in.Settings.UserFacingHost,
		Username:        in.Settings.Username,
		VcsChecks:       api.VCSCheck(in.Checks.Mode),
		VcsCheckOptions: checkOptionsView(in.Checks),
	}
	if d != nil {
		v.WebhookSecret, v.WebhookURL = d.WebhookSecret, d.WebhookURL
	}
	return v
}
