package http

import (
	"context"
	"errors"

	"github.com/weaveplatform/infraweave/internal/features/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// FeaturePreview answers the caller's feature previews.
//
// ref: decision 0021 § "Feature previews"
func (s Server) FeaturePreview(ctx context.Context, _ api.FeaturePreviewRequestObject) (api.FeaturePreviewResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.FeaturePreviews(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return api.FeaturePreview200JSONResponse{Body: previewBody(v, p)}, nil
}

// AccountSetFeaturePreviewsEnabled sets the account's opt-in, which the
// route table admits for account admins alone.
func (s Server) AccountSetFeaturePreviewsEnabled(
	ctx context.Context,
	req api.AccountSetFeaturePreviewsEnabledRequestObject,
) (api.AccountSetFeaturePreviewsEnabledResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.SetAccountFeaturePreviews(ctx, p, req.Body.Enabled)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.AccountSetFeaturePreviewsEnabled200JSONResponse{Body: previewBody(v, p)}, nil
}

// FeaturePreviewSetPersonalEnabled sets the calling member's own setting,
// answering 403 forbidden to a principal that names no member.
func (s Server) FeaturePreviewSetPersonalEnabled(
	ctx context.Context,
	req api.FeaturePreviewSetPersonalEnabledRequestObject,
) (api.FeaturePreviewSetPersonalEnabledResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	v, err := s.svc.SetPersonalFeaturePreviews(ctx, p, req.Body.Enabled)
	if errors.Is(err, core.ErrNoMember) {
		//nolint:wrapcheck // a problem, answered as it is
		return nil, problem.Forbidden.Error(
			"Only a member has a personal feature preview setting; " +
				"an API key that is not personal, or a run, has none.")
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	return api.FeaturePreviewSetPersonalEnabled200JSONResponse{Body: previewBody(v, p)}, nil
}

// previewBody is v as p reads it: canManage is whether p is an account
// admin, which the authentication middleware decides at each request.
func previewBody(v core.Previews, p kernel.Principal) api.FeaturePreview {
	body := api.FeaturePreview{
		Account:  api.FeaturePreviewAccountSettings{Enabled: v.Account, CanManage: p.Admin},
		Features: make([]api.FeaturePreviewFeature, 0, len(v.Features)),
		Visible:  len(v.Features) > 0,
	}
	for _, f := range v.Features {
		out := api.FeaturePreviewFeature{
			Key:         f.Name,
			Name:        f.Preview.Name,
			Description: f.Preview.Description,
			DocsUrl:     f.Preview.DocsURL,
			Active:      f.Active,
		}
		if img := f.Preview.ImageURL; img != "" {
			out.ImageUrl = &img
		}
		body.Features = append(body.Features, out)
	}
	if v.Personal != nil {
		body.Personal = &api.FeaturePreviewPersonalSettings{Enabled: *v.Personal}
	}
	return body
}
