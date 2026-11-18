package http

import (
	"context"
	"maps"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
)

// gitHubUpdateInput keeps write-only fields optional in the PATCH input
// view. A PUT supplies its required webhook secret explicitly.
type gitHubUpdateInput struct {
	APIHost        string                       `json:"apiHost"`
	CustomInput    api.CustomVCSUpdateInputBody `json:"customInput"`
	PrivateKey     *string                      `json:"privateKey,omitempty"`
	UserFacingHost *string                      `json:"userFacingHost,omitempty"`
	WebhookSecret  *string                      `json:"webhookSecret,omitempty"`
}

// GitlabIntegrationPatch merges its named members into the PUT input,
// preserving the version read until the shared update commits.
func (s Server) GitlabIntegrationPatch(ctx context.Context, req api.GitlabIntegrationPatchRequestObject) (api.GitlabIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	read := func() (api.GitlabIntegrationUpdateRequest, int64, error) {
		cur, custom, err := s.patchView(ctx, p, req.GitlabIntegrationId, integration.GitLab)
		if err != nil {
			return api.GitlabIntegrationUpdateRequest{}, 0, err
		}
		return api.GitlabIntegrationUpdateRequest{
			CustomInput: custom, ApiHost: &cur.Settings.APIHost, UserFacingHost: &cur.Settings.UserFacingHost,
		}, cur.Version, nil
	}
	write := func(in api.GitlabIntegrationUpdateRequest, expect kernel.Expect) (integration.Integration, error) {
		return s.updateGitLab(ctx, p, req.GitlabIntegrationId, expect, in)
	}
	var patch map[string]any
	if req.Body != nil {
		patch = providerPatch(*req.Body)
	}
	out, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // the shared PUT path names its errors
	}
	v, err := s.gitlab(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GitlabIntegrationPatch200JSONResponse{
		Body: v, Headers: api.GitlabIntegrationPatch200ResponseHeaders{ETag: new(strict.ETag(out.Version))},
	}, nil
}

// BitbucketCloudIntegrationPatch merges its named members into the PUT input,
// preserving the version read until the shared update commits.
func (s Server) BitbucketCloudIntegrationPatch(ctx context.Context, req api.BitbucketCloudIntegrationPatchRequestObject) (api.BitbucketCloudIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	read := func() (api.BitbucketCloudIntegrationUpdateInputBody, int64, error) {
		cur, custom, err := s.patchView(ctx, p, req.BitbucketCloudIntegrationId, integration.BitbucketCloud)
		if err != nil {
			return api.BitbucketCloudIntegrationUpdateInputBody{}, 0, err
		}
		return api.BitbucketCloudIntegrationUpdateInputBody{
			CustomInput: custom, Email: cur.Settings.Email,
		}, cur.Version, nil
	}
	write := func(in api.BitbucketCloudIntegrationUpdateInputBody, expect kernel.Expect) (integration.Integration, error) {
		return s.updateBitbucketCloud(ctx, p, req.BitbucketCloudIntegrationId, expect, in)
	}
	var patch map[string]any
	if req.Body != nil {
		patch = providerPatch(*req.Body)
	}
	out, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // the shared PUT path names its errors
	}
	v, err := s.bitbucketCloud(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketCloudIntegrationPatch200JSONResponse{
		Body: v, Headers: api.BitbucketCloudIntegrationPatch200ResponseHeaders{ETag: new(strict.ETag(out.Version))},
	}, nil
}

// BitbucketDatacenterIntegrationPatch merges its named members into the PUT input,
// preserving the version read until the shared update commits.
func (s Server) BitbucketDatacenterIntegrationPatch(
	ctx context.Context,
	req api.BitbucketDatacenterIntegrationPatchRequestObject,
) (api.BitbucketDatacenterIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	read := func() (api.BitbucketDatacenterIntegrationUpdateRequest, int64, error) {
		cur, custom, err := s.patchView(ctx, p, req.BitbucketDatacenterIntegrationId, integration.BitbucketDC)
		if err != nil {
			return api.BitbucketDatacenterIntegrationUpdateRequest{}, 0, err
		}
		return api.BitbucketDatacenterIntegrationUpdateRequest{
			CustomInput: custom, ApiHost: cur.Settings.APIHost, UserFacingHost: cur.Settings.UserFacingHost,
			Username: &cur.Settings.Username,
		}, cur.Version, nil
	}
	write := func(in api.BitbucketDatacenterIntegrationUpdateRequest, expect kernel.Expect) (integration.Integration, error) {
		return s.updateBitbucketDC(ctx, p, req.BitbucketDatacenterIntegrationId, expect, in)
	}
	var patch map[string]any
	if req.Body != nil {
		patch = providerPatch(*req.Body)
	}
	out, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // the shared PUT path names its errors
	}
	v, err := s.bitbucketDC(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.BitbucketDatacenterIntegrationPatch200JSONResponse{
		Body: v, Headers: api.BitbucketDatacenterIntegrationPatch200ResponseHeaders{ETag: new(strict.ETag(out.Version))},
	}, nil
}

// AzureDevOpsRepoIntegrationPatch merges its named members into the PUT input,
// preserving the version read until the shared update commits.
func (s Server) AzureDevOpsRepoIntegrationPatch(
	ctx context.Context,
	req api.AzureDevOpsRepoIntegrationPatchRequestObject,
) (api.AzureDevOpsRepoIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	read := func() (api.AzureDevOpsRepoIntegrationUpdateRequest, int64, error) {
		cur, custom, err := s.patchView(ctx, p, req.AzureDevOpsRepoIntegrationId, integration.AzureDevOps)
		if err != nil {
			return api.AzureDevOpsRepoIntegrationUpdateRequest{}, 0, err
		}
		return api.AzureDevOpsRepoIntegrationUpdateRequest{
			CustomInput: custom, OrganizationURL: cur.Settings.OrganizationURL, UserFacingHost: &cur.Settings.UserFacingHost,
			AccessibleProjects: &cur.Settings.AccessibleProjects,
		}, cur.Version, nil
	}
	write := func(in api.AzureDevOpsRepoIntegrationUpdateRequest, expect kernel.Expect) (integration.Integration, error) {
		return s.updateAzureDevOps(ctx, p, req.AzureDevOpsRepoIntegrationId, expect, in)
	}
	var patch map[string]any
	if req.Body != nil {
		patch = providerPatch(*req.Body)
	}
	out, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // the shared PUT path names its errors
	}
	v, err := s.azureDevOps(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.AzureDevOpsRepoIntegrationPatch200JSONResponse{
		Body: v, Headers: api.AzureDevOpsRepoIntegrationPatch200ResponseHeaders{ETag: new(strict.ETag(out.Version))},
	}, nil
}

// GithubEnterpriseIntegrationPatch merges its named members into the PUT input,
// preserving the version read until the shared update commits.
func (s Server) GithubEnterpriseIntegrationPatch(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationPatchRequestObject,
) (api.GithubEnterpriseIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	read := func() (gitHubUpdateInput, int64, error) {
		cur, custom, err := s.patchView(ctx, p, req.GithubEnterpriseIntegrationId, integration.GitHubEnterprise)
		if err != nil {
			return gitHubUpdateInput{}, 0, err
		}
		return gitHubUpdateInput{
			CustomInput: custom, APIHost: cur.Settings.APIHost, UserFacingHost: &cur.Settings.UserFacingHost,
		}, cur.Version, nil
	}
	write := func(in gitHubUpdateInput, expect kernel.Expect) (integration.Integration, error) {
		return s.updateGitHub(ctx, p, req.GithubEnterpriseIntegrationId, expect, in)
	}
	var patch map[string]any
	if req.Body != nil {
		patch = providerPatch(*req.Body)
	}
	out, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // the shared PUT path names its errors
	}
	v, err := s.enterprise(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegrationPatch200JSONResponse{
		Body: v, Headers: api.GithubEnterpriseIntegrationPatch200ResponseHeaders{ETag: new(strict.ETag(out.Version))},
	}, nil
}

// patchView projects only supported PUT fields. Sealed credentials stay
// absent so an unrelated patch cannot rotate or expose them.
func (s Server) patchView(ctx context.Context, p kernel.Principal, id, provider string) (integration.Integration, api.CustomVCSUpdateInputBody, error) {
	cur, _, err := s.svc.Read(ctx, p, id, provider)
	if err != nil {
		return cur, api.CustomVCSUpdateInputBody{}, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return cur, api.CustomVCSUpdateInputBody{}, err //nolint:wrapcheck // the access domain names the lookup
	}
	return cur, api.CustomVCSUpdateInputBody{
		Space: slugs[cur.Space], Description: &cur.Description, Labels: &cur.Labels,
		UseGitCheckout: &cur.UseGitCheckout, VcsChecks: new(api.VCSCheck(cur.Checks.Mode)),
		VcsCheckOptions: &api.VCSCheckOptionsInput{
			IndividualFailuresOnly:  &cur.Checks.IndividualFailuresOnly,
			IndividualSkipUnchanged: &cur.Checks.IndividualSkipUnchanged,
		},
	}, nil
}

// providerPatch resets only explicitly removed public custom settings;
// an absent write-only field remains absent in the merged input.
func providerPatch(patch map[string]any) map[string]any {
	return resetRemoved(patch, map[string]any{
		"customInput": map[string]any{
			"description": "", "labels": []any{}, "useGitCheckout": false,
			"vcsChecks": "INDIVIDUAL",
			"vcsCheckOptions": map[string]any{
				"individualFailuresOnly": false, "individualSkipUnchanged": false,
			},
		},
	})
}

func resetRemoved(patch, defaults map[string]any) map[string]any {
	out := maps.Clone(patch)
	for name, value := range out {
		fallback, known := defaults[name]
		if !known {
			continue
		}
		if value == nil {
			out[name] = fallback
			continue
		}
		object, objectOK := value.(map[string]any)
		nested, nestedOK := fallback.(map[string]any)
		if objectOK && nestedOK {
			out[name] = resetRemoved(object, nested)
		}
	}
	return out
}
