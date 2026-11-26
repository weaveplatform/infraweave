package http

import (
	"context"
	"encoding/base64"

	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// azurePageOf is all as a page of views, with the cursor of the next page
// when more follows.
func (s Server) azurePageOf(ctx context.Context, acct kernel.AccountID, all []core.AzureIntegration, more bool) (api.AzureIntegrationPage, error) {
	views, err := s.azureViews(ctx, acct, all...)
	if err != nil {
		return api.AzureIntegrationPage{}, err
	}
	out := api.AzureIntegrationPage{Items: views}
	if more && len(all) > 0 {
		next := base64.RawURLEncoding.EncodeToString([]byte(all[len(all)-1].Name))
		out.NextCursor = &next
	}
	return out, nil
}

// AzureIntegrations lists the Azure integrations of the caller's account
// in the spaces it may read.
func (s Server) AzureIntegrations(ctx context.Context, req api.AzureIntegrationsRequestObject) (api.AzureIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	all, more, err := s.svc.ListAzure(ctx, p, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.azurePageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrations200JSONResponse{Body: out}, nil
}

// AttachableAzureIntegrationsInSpace lists the Azure integrations a stack
// in the space may attach that the caller may read.
func (s Server) AttachableAzureIntegrationsInSpace(
	ctx context.Context,
	req api.AttachableAzureIntegrationsInSpaceRequestObject,
) (api.AttachableAzureIntegrationsInSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	all, more, err := s.svc.AzureAttachable(ctx, p, req.Params.Id, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.azurePageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.AttachableAzureIntegrationsInSpace200JSONResponse{Body: out}, nil
}

// StackAttachableAzureIntegrations lists the Azure integrations the stack
// may attach that the caller may read.
func (s Server) StackAttachableAzureIntegrations(
	ctx context.Context,
	req api.StackAttachableAzureIntegrationsRequestObject,
) (api.StackAttachableAzureIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	all, more, err := s.svc.StackAzureAttachable(ctx, p, req.StackId, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.azurePageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.StackAttachableAzureIntegrations200JSONResponse{Body: out}, nil
}

// AzureIntegration answers one Azure integration of the caller's account.
func (s Server) AzureIntegration(ctx context.Context, req api.AzureIntegrationRequestObject) (api.AzureIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.GetAzure(ctx, p.AccountID, req.AzureIntegrationId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegration200JSONResponse{
		Body: v, Headers: api.AzureIntegration200ResponseHeaders{ETag: azureETag(i)},
	}, nil
}

// AzureIntegrationByName answers one Azure integration of the caller's
// account by its name.
func (s Server) AzureIntegrationByName(ctx context.Context, req api.AzureIntegrationByNameRequestObject) (api.AzureIntegrationByNameResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.AzureByName(ctx, p, req.Params.Name)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationByName200JSONResponse{Body: v}, nil
}

// AzureIntegrationCreate creates an Azure integration, with its Entra
// application, in the space its body names.
func (s Server) AzureIntegrationCreate(ctx context.Context, req api.AzureIntegrationCreateRequestObject) (api.AzureIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	i, err := s.svc.CreateAzure(ctx, p, core.AzureInput{
		Name: b.Name, Space: deref(b.Space), TenantID: b.TenantID,
		DefaultSubscription: b.DefaultSubscriptionId, Labels: b.Labels,
		Autoattach: deref(b.AutoattachEnabled),
	})
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationCreate201JSONResponse{Body: v}, nil
}

// azureUpdateInput is an update's body as an input.
func azureUpdateInput(b api.AzureIntegrationUpdateRequest) core.AzureInput {
	return core.AzureInput{
		Name: b.Name, Space: deref(b.Space), DefaultSubscription: b.DefaultSubscriptionId,
		Labels: b.Labels, Autoattach: deref(b.AutoattachEnabled),
	}
}

// AzureIntegrationUpdate replaces an Azure integration's settings with its
// body's.
func (s Server) AzureIntegrationUpdate(ctx context.Context, req api.AzureIntegrationUpdateRequestObject) (api.AzureIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.UpdateAzure(ctx, p, req.AzureIntegrationId, strict.IfMatch(req.Params.IfMatch),
		azureUpdateInput(*req.Body))
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationUpdate200JSONResponse{
		Body: v, Headers: api.AzureIntegrationUpdate200ResponseHeaders{ETag: azureETag(i)},
	}, nil
}

// AzureIntegrationPatch changes the settings its merge patch names and
// keeps the rest, writing the result as AzureIntegrationUpdate writes its
// body.
func (s Server) AzureIntegrationPatch(ctx context.Context, req api.AzureIntegrationPatchRequestObject) (api.AzureIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.AzureIntegrationUpdateRequest, int64, error) {
		i, err := s.svc.GetAzure(ctx, p.AccountID, req.AzureIntegrationId)
		if err != nil {
			return api.AzureIntegrationUpdateRequest{}, 0, err //nolint:wrapcheck // answered by cloudError
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.AzureIntegrationUpdateRequest{}, 0, err //nolint:wrapcheck // the access domain names it
		}
		return azureInputView(i, slugs[i.Space]), i.Version, nil
	}
	write := func(body api.AzureIntegrationUpdateRequest, expect kernel.Expect) (core.AzureIntegration, error) {
		return s.svc.UpdateAzure(ctx, p, req.AzureIntegrationId, expect, azureUpdateInput(body)) //nolint:wrapcheck // as above
	}
	i, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationPatch200JSONResponse{
		Body: v, Headers: api.AzureIntegrationPatch200ResponseHeaders{ETag: azureETag(i)},
	}, nil
}

// AzureIntegrationDelete deletes an Azure integration with its Entra
// application, and answers it as it was.
func (s Server) AzureIntegrationDelete(ctx context.Context, req api.AzureIntegrationDeleteRequestObject) (api.AzureIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	// The view reads the integration's attachments, which the delete takes
	// with it, so it is read first.
	i, err := s.svc.GetAzure(ctx, p.AccountID, req.AzureIntegrationId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureView(ctx, i)
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.DeleteAzure(ctx, p, req.AzureIntegrationId,
		strict.IfMatch(req.Params.IfMatch)); err != nil {
		return nil, cloudError(err)
	}
	return api.AzureIntegrationDelete200JSONResponse{Body: v}, nil
}

// AzureIntegrationsEnabled answers whether the caller's account may use
// Azure integrations.
func (s Server) AzureIntegrationsEnabled(ctx context.Context, _ api.AzureIntegrationsEnabledRequestObject) (api.AzureIntegrationsEnabledResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := s.svc.AzureEnabled(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the features domain names the read
	}
	return api.AzureIntegrationsEnabled200JSONResponse{Body: ok}, nil
}

// AzureIntegrationAttachedStack answers one of an Azure integration's
// attachments.
func (s Server) AzureIntegrationAttachedStack(ctx context.Context, req api.AzureIntegrationAttachedStackRequestObject) (api.AzureIntegrationAttachedStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.svc.AzureAttachedStack(ctx, p, req.AzureIntegrationId, req.Params.Id)
	if err != nil {
		return nil, cloudError(err)
	}
	st, err := s.stacks.Get(ctx, a.Account, a.Stack)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stacks domain names the read
	}
	v, err := s.azureAttachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationAttachedStack200JSONResponse{Body: v}, nil
}

// AzureIntegrationAttach attaches an Azure integration to a stack.
func (s Server) AzureIntegrationAttach(ctx context.Context, req api.AzureIntegrationAttachRequestObject) (api.AzureIntegrationAttachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	a, st, err := s.svc.AttachAzure(ctx, p, req.StackId, req.AzureIntegrationId, b.Read, b.Write, b.SubscriptionId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureAttachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationAttach200JSONResponse{Body: v}, nil
}

// AzureIntegrationAttachmentUpdate changes the sides an Azure attachment
// serves and the subscription it uses.
func (s Server) AzureIntegrationAttachmentUpdate(
	ctx context.Context,
	req api.AzureIntegrationAttachmentUpdateRequestObject,
) (api.AzureIntegrationAttachmentUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	a, st, err := s.svc.UpdateAzureAttachment(ctx, p, req.AttachmentId, b.Read, b.Write, b.SubscriptionId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureAttachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationAttachmentUpdate200JSONResponse{Body: v}, nil
}

// AzureIntegrationDetach detaches an Azure integration from a stack and
// answers the attachment as it was.
func (s Server) AzureIntegrationDetach(ctx context.Context, req api.AzureIntegrationDetachRequestObject) (api.AzureIntegrationDetachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.DetachAzure(ctx, p, req.AttachmentId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.azureAttachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AzureIntegrationDetach200JSONResponse{Body: v}, nil
}

// AzureIntegrationConsentConfirm records the caller's statement that an
// admin of the customer's tenant consented to the integration's
// application.
func (s Server) AzureIntegrationConsentConfirm(ctx context.Context, req api.AzureIntegrationConsentConfirmRequestObject) (api.AzureIntegrationConsentConfirmResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.SetConsent(ctx, p, req.AzureIntegrationId, true)
	if err != nil {
		return nil, cloudError(err)
	}
	return api.AzureIntegrationConsentConfirm200JSONResponse{
		Body: api.AzureIntegrationConsent{AdminConsentProvided: i.ConsentProvided},
	}, nil
}

// AzureIntegrationConsentClear records the caller's statement that the
// integration's application no longer has admin consent.
func (s Server) AzureIntegrationConsentClear(ctx context.Context, req api.AzureIntegrationConsentClearRequestObject) (api.AzureIntegrationConsentClearResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.SetConsent(ctx, p, req.AzureIntegrationId, false)
	if err != nil {
		return nil, cloudError(err)
	}
	return api.AzureIntegrationConsentClear200JSONResponse{
		Body: api.AzureIntegrationConsent{AdminConsentProvided: i.ConsentProvided},
	}, nil
}
