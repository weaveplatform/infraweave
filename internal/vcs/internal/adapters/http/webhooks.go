package http

import (
	"context"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
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
	v, err := s.gitlab(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegrationRegenerateWebhookSecret200JSONResponse{Body: v}, nil
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
	v, err := s.bitbucketCloud(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegrationRegenerateWebhookSecret200JSONResponse{Body: v}, nil
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
	v, err := s.bitbucketDC(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegrationRegenerateWebhookSecret200JSONResponse{Body: v}, nil
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
	v, err := s.azureDevOps(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegrationRegenerateWebhookSecret200JSONResponse{Body: v}, nil
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
	v, err := s.enterprise(ctx, p, in, true)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegrationRegenerateWebhookSecret200JSONResponse{Body: v}, nil
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
