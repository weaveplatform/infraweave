package http

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

// GitlabIntegrationRegenerateWebhookSecret rotates the current secret and returns the admin view.
func (s Server) GitlabIntegrationRegenerateWebhookSecret(
	ctx context.Context,
	req api.GitlabIntegrationRegenerateWebhookSecretRequestObject,
) (api.GitlabIntegrationRegenerateWebhookSecretResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.RotateWebhookSecret(ctx, p, req.GitlabIntegrationId, integration.GitLab, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, details, err := s.rotationView(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegrationRegenerateWebhookSecret200JSONResponse{Body: gitlabView(in.Integration, slugs, &details)}, nil
}

// GitlabWebhooksEndpoint reads the configured integration's delivery URL.
func (s Server) GitlabWebhooksEndpoint(ctx context.Context, req api.GitlabWebhooksEndpointRequestObject) (api.GitlabWebhooksEndpointResponseObject, error) {
	endpoint, err := s.webhookEndpoint(ctx, integration.GitLab, "")
	if err != nil {
		return nil, err
	}
	return api.GitlabWebhooksEndpoint200JSONResponse{Body: endpoint}, nil
}

// BitbucketCloudIntegrationRegenerateWebhookSecret rotates the current secret and returns the admin view.
func (s Server) BitbucketCloudIntegrationRegenerateWebhookSecret(
	ctx context.Context,
	req api.BitbucketCloudIntegrationRegenerateWebhookSecretRequestObject,
) (api.BitbucketCloudIntegrationRegenerateWebhookSecretResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.RotateWebhookSecret(ctx, p, req.BitbucketCloudIntegrationId, integration.BitbucketCloud, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, details, err := s.rotationView(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegrationRegenerateWebhookSecret200JSONResponse{Body: bitbucketCloudView(in.Integration, slugs, &details)}, nil
}

// BitbucketCloudWebhooksEndpoint reads the configured integration's delivery URL.
func (s Server) BitbucketCloudWebhooksEndpoint(ctx context.Context, req api.BitbucketCloudWebhooksEndpointRequestObject) (api.BitbucketCloudWebhooksEndpointResponseObject, error) {
	endpoint, err := s.webhookEndpoint(ctx, integration.BitbucketCloud, "")
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudWebhooksEndpoint200JSONResponse{Body: endpoint}, nil
}

// BitbucketDatacenterIntegrationRegenerateWebhookSecret rotates the current secret and returns the admin view.
func (s Server) BitbucketDatacenterIntegrationRegenerateWebhookSecret(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationRegenerateWebhookSecretRequestObject,
) (api.BitbucketDatacenterIntegrationRegenerateWebhookSecretResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.RotateWebhookSecret(ctx, p, req.BitbucketDatacenterIntegrationId, integration.BitbucketDC, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, details, err := s.rotationView(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegrationRegenerateWebhookSecret200JSONResponse{Body: bitbucketDCView(in.Integration, slugs, &details)}, nil
}

// BitbucketDatacenterWebhooksEndpoint reads the configured integration's delivery URL.
func (s Server) BitbucketDatacenterWebhooksEndpoint(
	ctx context.Context,
	req api.BitbucketDatacenterWebhooksEndpointRequestObject,
) (api.BitbucketDatacenterWebhooksEndpointResponseObject, error) {
	endpoint, err := s.webhookEndpoint(ctx, integration.BitbucketDC, "")
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterWebhooksEndpoint200JSONResponse{Body: endpoint}, nil
}

// AzureDevOpsRepoIntegrationRegenerateWebhookSecret rotates the current secret and returns the admin view.
func (s Server) AzureDevOpsRepoIntegrationRegenerateWebhookSecret(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationRegenerateWebhookSecretRequestObject,
) (api.AzureDevOpsRepoIntegrationRegenerateWebhookSecretResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.RotateWebhookSecret(ctx, p, req.AzureDevOpsRepoIntegrationId, integration.AzureDevOps, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, details, err := s.rotationView(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegrationRegenerateWebhookSecret200JSONResponse{Body: azureDevOpsView(in.Integration, slugs, &details)}, nil
}

// AzureDevOpsWebhooksEndpoint reads the configured integration's delivery URL.
func (s Server) AzureDevOpsWebhooksEndpoint(ctx context.Context, req api.AzureDevOpsWebhooksEndpointRequestObject) (api.AzureDevOpsWebhooksEndpointResponseObject, error) {
	endpoint, err := s.webhookEndpoint(ctx, integration.AzureDevOps, "")
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsWebhooksEndpoint200JSONResponse{Body: endpoint}, nil
}

// GithubEnterpriseIntegrationRegenerateWebhookSecret rotates the current secret and returns the admin view.
func (s Server) GithubEnterpriseIntegrationRegenerateWebhookSecret(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationRegenerateWebhookSecretRequestObject,
) (api.GithubEnterpriseIntegrationRegenerateWebhookSecretResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.RotateWebhookSecret(ctx, p, req.GithubEnterpriseIntegrationId, integration.GitHubEnterprise, accessapi.ClientAddr(ctx))
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, details, err := s.rotationView(ctx, p, in)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegrationRegenerateWebhookSecret200JSONResponse{Body: enterpriseView(in.Integration, slugs, &details, true)}, nil
}

// GithubEnterpriseWebhooksEndpoint reads the configured integration's delivery URL.
func (s Server) GithubEnterpriseWebhooksEndpoint(
	ctx context.Context,
	req api.GithubEnterpriseWebhooksEndpointRequestObject,
) (api.GithubEnterpriseWebhooksEndpointResponseObject, error) {
	endpoint, err := s.webhookEndpoint(ctx, integration.GitHubEnterprise, or(req.Params.Id, ""))
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseWebhooksEndpoint200JSONResponse{Body: endpoint}, nil
}

func (s Server) webhookEndpoint(ctx context.Context, provider, id string) (string, error) {
	p, err := principal(ctx)
	if err != nil {
		return "", err
	}
	endpoint, err := s.svc.WebhookEndpoint(ctx, p, provider, id)
	return endpoint, integrationError(err)
}

func (s Server) rotationView(ctx context.Context, p kernel.Principal, in integration.Rotation) (map[kernel.ULID]string, integration.Details, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, integration.Details{}, err //nolint:wrapcheck // the access domain names the read
	}
	return slugs, s.svc.RotationDetails(ctx, in), nil
}
