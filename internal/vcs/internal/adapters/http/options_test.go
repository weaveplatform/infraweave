package http_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	vcshttp "github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/http"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

type deniedOption struct{ refusal *featuresapi.Refusal }

func (d deniedOption) AllowOrganization(context.Context, kernel.OrganizationID, string) error {
	return d.refusal
}

// Typed handlers must preserve the domain's refusal for values the
// early body gate cannot see, including omission and JSON null.
func TestUnitCreateOptionProblems(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		r := &featuresapi.Refusal{Feature: "multiple-vcs-integrations", Tiers: []string{"Enterprise"}, Disabled: disabled}
		srv := vcshttp.New(integration.New(integration.Deps{
			Features: deniedOption{r}, Providers: []string{
				integration.GitLab, integration.GitHubEnterprise, integration.BitbucketCloud,
				integration.BitbucketDC, integration.AzureDevOps,
			},
		}), nil, nil)
		ctx := kernel.WithPrincipal(t.Context(), kernel.Principal{OrganizationID: kernel.NewOrganizationID()})
		no := false
		for _, value := range []*bool{nil, &no} {
			custom := &api.CustomVCSInput{Name: "Main", IsDefault: value}
			calls := map[string]func() error{
				"gitlab": func() error {
					_, err := srv.GitlabIntegrationCreate(ctx, api.GitlabIntegrationCreateRequestObject{
						Body: &api.GitlabIntegrationCreateJSONRequestBody{CustomInput: custom},
					})
					return err
				},
				"bitbucket-cloud-v2": func() error {
					_, err := srv.BitbucketCloudIntegrationCreateV2(ctx, api.BitbucketCloudIntegrationCreateV2RequestObject{
						Body: &api.BitbucketCloudIntegrationCreateV2JSONRequestBody{Email: "dev@acme.example", CustomInput: custom},
					})
					return err
				},
				"bitbucket-datacenter": func() error {
					_, err := srv.BitbucketDatacenterIntegrationCreate(ctx, api.BitbucketDatacenterIntegrationCreateRequestObject{
						Body: &api.BitbucketDatacenterIntegrationCreateJSONRequestBody{CustomInput: custom},
					})
					return err
				},
				"azure-devops": func() error {
					_, err := srv.AzureDevOpsRepoIntegrationCreate(ctx, api.AzureDevOpsRepoIntegrationCreateRequestObject{
						Body: &api.AzureDevOpsRepoIntegrationCreateJSONRequestBody{OrganizationURL: "https://dev.azure.com/acme", CustomInput: custom},
					})
					return err
				},
				"github-enterprise": func() error {
					_, err := srv.GithubEnterpriseIntegrationCreate(ctx, api.GithubEnterpriseIntegrationCreateRequestObject{
						Body: &api.GithubEnterpriseIntegrationCreateJSONRequestBody{CustomInput: custom},
					})
					return err
				},
			}
			for name, call := range calls {
				pe, ok := errors.AsType[*problem.Error](call())
				if !ok || pe.Problem.Status != http.StatusForbidden || pe.Problem.Feature != r.Feature {
					t.Fatalf("%s refusal: %+v", name, pe)
				}
				want, tiers := problem.FeatureNotInTier, r.Tiers
				if disabled {
					want, tiers = problem.FeatureDisabled, nil
				}
				if pe.Problem.Type != problem.TypePrefix+want.Slug || !reflect.DeepEqual(pe.Problem.Tiers, tiers) {
					t.Fatalf("%s refusal shape: %+v", name, pe.Problem)
				}
			}
		}
	}
}
