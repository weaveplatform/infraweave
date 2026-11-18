package http_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	vcshttp "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
)

func TestUnitWebhookControlsRequirePrincipal(t *testing.T) {
	s := vcshttp.New(nil, nil, nil)
	ctx := t.Context()
	calls := map[string]func() error{
		"gitlab rotate": func() error {
			_, err := s.GitlabIntegrationRegenerateWebhookSecret(ctx, api.GitlabIntegrationRegenerateWebhookSecretRequestObject{})
			return err
		},
		"gitlab URL": func() error {
			_, err := s.GitlabWebhooksEndpoint(ctx, api.GitlabWebhooksEndpointRequestObject{})
			return err
		},
		"cloud rotate": func() error {
			_, err := s.BitbucketCloudIntegrationRegenerateWebhookSecret(ctx, api.BitbucketCloudIntegrationRegenerateWebhookSecretRequestObject{})
			return err
		},
		"cloud URL": func() error {
			_, err := s.BitbucketCloudWebhooksEndpoint(ctx, api.BitbucketCloudWebhooksEndpointRequestObject{})
			return err
		},
		"DC rotate": func() error {
			_, err := s.BitbucketDatacenterIntegrationRegenerateWebhookSecret(ctx, api.BitbucketDatacenterIntegrationRegenerateWebhookSecretRequestObject{})
			return err
		},
		"DC URL": func() error {
			_, err := s.BitbucketDatacenterWebhooksEndpoint(ctx, api.BitbucketDatacenterWebhooksEndpointRequestObject{})
			return err
		},
		"Azure rotate": func() error {
			_, err := s.AzureDevOpsRepoIntegrationRegenerateWebhookSecret(ctx, api.AzureDevOpsRepoIntegrationRegenerateWebhookSecretRequestObject{})
			return err
		},
		"Azure URL": func() error {
			_, err := s.AzureDevOpsWebhooksEndpoint(ctx, api.AzureDevOpsWebhooksEndpointRequestObject{})
			return err
		},
		"App rotate": func() error {
			_, err := s.GithubEnterpriseIntegrationRegenerateWebhookSecret(ctx, api.GithubEnterpriseIntegrationRegenerateWebhookSecretRequestObject{})
			return err
		},
		"App URL": func() error {
			_, err := s.GithubEnterpriseWebhooksEndpoint(ctx, api.GithubEnterpriseWebhooksEndpointRequestObject{})
			return err
		},
	}
	for name, call := range calls {
		refused, ok := errors.AsType[*problem.Error](call())
		if !ok || refused.Problem.Type != problem.TypePrefix+problem.Unauthenticated.Slug {
			t.Fatalf("%s: %+v", name, refused)
		}
	}
}
