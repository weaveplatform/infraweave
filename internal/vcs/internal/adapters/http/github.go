package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// GithubIntegration answers the operator's GitHub App integration with the
// caller's account's check options.
func (s Server) GithubIntegration(
	ctx context.Context,
	_ api.GithubIntegrationRequestObject,
) (api.GithubIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.Builtin(ctx, p, integration.GitHub)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.GithubIntegration200JSONResponse{Body: githubView(in)}, nil
}

// GithubIntegrationUpdate sets the caller's account's check options of the
// operator's GitHub App integration.
func (s Server) GithubIntegrationUpdate(
	ctx context.Context,
	req api.GithubIntegrationUpdateRequestObject,
) (api.GithubIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, err := s.svc.Builtin(ctx, p, integration.GitHub)
	if err != nil {
		return nil, integrationError(err)
	}
	checks := in.Checks
	checks.Mode = string(req.Body.VcsChecks)
	if o := req.Body.VcsCheckOptions; o != nil {
		checks = checkOptions(checks, o)
	}
	in, err = s.svc.SetBuiltin(ctx, p, integration.GitHub, checks, req.Body.UseGitCheckout)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.GithubIntegrationUpdate200JSONResponse{Body: githubView(in)}, nil
}

// GithubEnterpriseIntegrationCreate creates a GitHub Enterprise
// integration, with a webhook secret of its own when the request carries
// none.
func (s Server) GithubEnterpriseIntegrationCreate(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationCreateRequestObject,
) (api.GithubEnterpriseIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	secret := ""
	if b.WebhookSecret != nil {
		secret = *b.WebhookSecret
	}
	if secret == "" {
		secret = webhookSecret()
	}
	in := integration.Input{
		Provider: integration.GitHubEnterprise, Labels: []string{},
		Settings: port.Settings{
			APIHost: b.ApiHost, UserFacingHost: or(b.UserFacingHost, b.ApiHost), AppID: b.AppID,
		},
		Secrets: &integration.Secrets{
			Credential: []byte(b.PrivateKey), WebhookSecret: []byte(secret),
		},
	}
	if c := b.CustomInput; c != nil {
		in.Name, in.Space, in.Description = c.Name, c.SpaceID, or(c.Description, "")
		in.IsDefault = c.IsDefault != nil && *c.IsDefault
		if c.Labels != nil {
			in.Labels = *c.Labels
		}
		in.UseGitCheckout = c.UseGitCheckout != nil && *c.UseGitCheckout
		if c.VcsChecks != nil {
			in.Checks.Mode = string(*c.VcsChecks)
		}
		if c.VcsCheckOptions != nil {
			in.Checks = checkOptions(in.Checks, c.VcsCheckOptions)
		}
	}
	out, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.enterprise(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegrationCreate201JSONResponse{Body: v}, nil
}

// GithubEnterpriseIntegration answers one GitHub Enterprise integration,
// with its webhook secret and URL when the caller administers its space.
func (s Server) GithubEnterpriseIntegration(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationRequestObject,
) (api.GithubEnterpriseIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in, admin, err := s.svc.Read(
		ctx, p, req.GithubEnterpriseIntegrationId, integration.GitHubEnterprise,
	)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.enterprise(ctx, p, in, admin)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegration200JSONResponse{Body: v}, nil
}

// GithubEnterpriseIntegrationUpdate replaces a GitHub Enterprise
// integration's settings, keeping its private key when the request
// carries none and each custom field the request leaves out.
func (s Server) GithubEnterpriseIntegrationUpdate(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationUpdateRequestObject,
) (api.GithubEnterpriseIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	id, b := req.GithubEnterpriseIntegrationId, req.Body
	cur, _, err := s.svc.Read(ctx, p, id, integration.GitHubEnterprise)
	if err != nil {
		return nil, integrationError(err)
	}
	c := b.CustomInput
	in := integration.Input{
		Provider: integration.GitHubEnterprise, Space: c.Space,
		Description: or(c.Description, cur.Description), Labels: cur.Labels,
		Checks: cur.Checks, UseGitCheckout: cur.UseGitCheckout,
		Settings: port.Settings{
			APIHost: b.ApiHost, UserFacingHost: or(b.UserFacingHost, b.ApiHost),
			AppID: cur.Settings.AppID,
		},
		Secrets: &integration.Secrets{WebhookSecret: []byte(b.WebhookSecret)},
	}
	if b.PrivateKey != nil {
		in.Secrets.Credential = []byte(*b.PrivateKey)
	}
	if c.Labels != nil {
		in.Labels = *c.Labels
	}
	if c.UseGitCheckout != nil {
		in.UseGitCheckout = *c.UseGitCheckout
	}
	if c.VcsChecks != nil {
		in.Checks.Mode = string(*c.VcsChecks)
	}
	if c.VcsCheckOptions != nil {
		in.Checks = checkOptions(in.Checks, c.VcsCheckOptions)
	}
	out, err := s.svc.Update(ctx, p, id, strict.IfMatch(req.Params.IfMatch), in)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.enterprise(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GithubEnterpriseIntegrationUpdate200JSONResponse{Body: v}, nil
}

// GithubEnterpriseIntegrationDelete deletes a GitHub Enterprise
// integration, detaching the stacks bound to it, and answers it as it
// was, without its App's state, which its deleted key can no longer read.
func (s Server) GithubEnterpriseIntegrationDelete(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationDeleteRequestObject,
) (api.GithubEnterpriseIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.Delete(
		ctx, p, req.GithubEnterpriseIntegrationId, integration.GitHubEnterprise,
	)
	if err != nil {
		return nil, integrationError(err)
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	return api.GithubEnterpriseIntegrationDelete200JSONResponse{
		Body: enterpriseView(out, slugs, nil, false),
	}, nil
}

// GithubEnterpriseIntegrationTest answers whether every check of a GitHub
// Enterprise integration's connection test passes.
func (s Server) GithubEnterpriseIntegrationTest(
	ctx context.Context,
	req api.GithubEnterpriseIntegrationTestRequestObject,
) (api.GithubEnterpriseIntegrationTestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	checks, err := s.svc.Test(
		ctx, p, req.GithubEnterpriseIntegrationId, integration.GitHubEnterprise,
	)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.GithubEnterpriseIntegrationTest200JSONResponse{
		Body: testResult(checks).Status == api.SUCCESS,
	}, nil
}

// GithubAppGenerateManifest opens a GitHub App manifest form. An
// organization's App names its owner.
func (s Server) GithubAppGenerateManifest(
	ctx context.Context,
	req api.GithubAppGenerateManifestRequestObject,
) (api.GithubAppGenerateManifestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	owner := or(b.OwnerName, "")
	if b.OwnerType == api.AccountTypeORG && owner == "" {
		return nil, &kernel.Invalid{
			Detail: "The manifest form's owner is refused.",
			Errors: []kernel.FieldError{{
				Pointer: "/ownerName", Reason: kernel.ReasonRequired,
				Message: "An organization's App names the organization.",
			}},
		}
	}
	if b.OwnerType != api.AccountTypeORG {
		owner = ""
	}
	m, err := s.svc.OpenManifest(ctx, p, or(b.Host, ""), or(b.ApiHost, ""), owner)
	if err != nil {
		return nil, integrationError(err)
	}
	return api.GithubAppGenerateManifest200JSONResponse{
		Body: api.GitHubAppManifest{Endpoint: m.Endpoint, Manifest: m.Manifest},
	}, nil
}

// GithubAppCreateFromManifest creates a GitHub Enterprise integration from
// the App a manifest form created.
func (s Server) GithubAppCreateFromManifest(
	ctx context.Context,
	req api.GithubAppCreateFromManifestRequestObject,
) (api.GithubAppCreateFromManifestResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.svc.FromManifest(ctx, p, req.Body.Code, req.Body.State)
	if err != nil {
		return nil, integrationError(err)
	}
	v, err := s.enterprise(ctx, p, out, true)
	if err != nil {
		return nil, err
	}
	return api.GithubAppCreateFromManifest201JSONResponse{Body: v}, nil
}

// enterprise is in as the GitHub Enterprise operations answer it, with
// its App as its host reports it now, and its webhook secret and URL when
// admin is set.
func (s Server) enterprise(
	ctx context.Context,
	p kernel.Principal,
	in integration.Integration,
	admin bool,
) (api.GithubEnterpriseIntegration, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.GithubEnterpriseIntegration{}, err //nolint:wrapcheck // the access domain names the read
	}
	d, err := s.svc.Details(ctx, in)
	if err != nil {
		return api.GithubEnterpriseIntegration{}, err //nolint:wrapcheck // the store names the read
	}
	return enterpriseView(in, slugs, &d, admin), nil
}

// enterpriseView is in as the GitHub Enterprise operations answer it, its
// space named by its slug in slugs, with d's App when d has one, and d's
// webhook secret and URL when admin is set.
func enterpriseView(
	in integration.Integration,
	slugs map[kernel.ULID]string,
	d *integration.Details,
	admin bool,
) api.GithubEnterpriseIntegration {
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	v := api.GithubEnterpriseIntegration{
		ApiHost: in.Settings.APIHost, AppID: in.Settings.AppID, Description: in.Description,
		Id: in.ID.String(), IsDefault: in.IsDefault, Labels: labels, Name: in.Name,
		Kind:           api.GithubEnterpriseIntegrationKindGithubEnterpriseIntegration,
		Space:          &api.SpaceRef{Id: slugs[in.Space]},
		UseGitCheckout: in.UseGitCheckout, UserFacingHost: in.Settings.UserFacingHost,
		VcsChecks:       api.VCSCheck(in.Checks.Mode),
		VcsCheckOptions: checkOptionsView(in.Checks),
		Installations:   []api.GithubEnterpriseIntegrationInstallation{},
		GithubAppStatus: api.GitHubAppStatus(port.AppError),
	}
	if d == nil {
		return v
	}
	if d.App != nil {
		v.GithubAppStatus = api.GitHubAppStatus(d.App.Status)
		v.GithubAppExists = d.App.Status == port.AppOK
		v.AppUrl = d.App.URL
		for _, i := range d.App.Installations {
			v.Installations = append(v.Installations, api.GithubEnterpriseIntegrationInstallation{
				AccountName: i.Account, AccountType: nonEmpty(i.AccountType),
				InstallationId: strconv.FormatInt(i.ID, 10), InstallationUrl: nonEmpty(i.URL),
			})
		}
	}
	if admin {
		v.WebhookSecret, v.WebhookUrl = d.WebhookSecret, d.WebhookURL
	}
	return v
}

// githubView is in, the operator's App integration, as its operations
// answer it. Its host is GitHub Enterprise Server when the operator's App
// lives on one.
func githubView(in integration.Integration) api.GithubIntegration {
	host := in.Settings.APIHost
	return api.GithubIntegration{
		ApiHost: host, IsEnterprise: host != "" && host != "https://api.github.com",
		Kind:           api.GithubIntegrationKindGithubIntegration,
		UseGitCheckout: in.UseGitCheckout, VcsChecks: api.VCSCheck(in.Checks.Mode),
		VcsCheckOptions: checkOptionsView(in.Checks),
	}
}

func checkOptionsView(c integration.Checks) *api.VCSCheckOptions {
	return &api.VCSCheckOptions{
		IndividualFailuresOnly:  c.IndividualFailuresOnly,
		IndividualSkipUnchanged: c.IndividualSkipUnchanged,
	}
}

// checkOptions is c with each option o sets.
func checkOptions(c integration.Checks, o *api.VCSCheckOptionsInput) integration.Checks {
	if o.IndividualFailuresOnly != nil {
		c.IndividualFailuresOnly = *o.IndividualFailuresOnly
	}
	if o.IndividualSkipUnchanged != nil {
		c.IndividualSkipUnchanged = *o.IndividualSkipUnchanged
	}
	return c
}

// webhookSecret is a new webhook secret: 32 random bytes, hex-encoded, as
// GitHub accepts it in an App's settings.
func webhookSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// or is *p, or def when p is nil or empty.
func or(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
