package http

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/cloud/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Server is the cloud domain's partial of the API's strict server. The
// route table has decided each operation's action on the integration's
// space, the stack's for an attachment, or on the space a create or a
// list names, before its handler runs, and answered 404 for one the
// caller cannot read.
type Server struct {
	svc    *core.Service
	spaces accessapi.Spaces
	stacks stacksapi.Stacks
}

// New returns the partial over the service, naming spaces through spaces
// and stacks through stacks.
func New(svc *core.Service, spaces accessapi.Spaces, stacks stacksapi.Stacks) Server {
	return Server{svc: svc, spaces: spaces, stacks: stacks}
}

func principal(ctx context.Context) (kernel.Principal, error) {
	p, ok := kernel.PrincipalFrom(ctx)
	if !ok {
		//nolint:wrapcheck // a problem, answered as it is
		return kernel.Principal{}, problem.Unauthenticated.Error(
			"The request carries no valid credential.")
	}
	return p, nil
}

// The page sizes of a list: DefaultLimit when it names none, and any
// other clamped to 1 to MaxLimit.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// page is a list's cursor and limit as a page; a cursor no list gave is
// an invalid-cursor problem. A cursor is the last name of the previous
// page, encoded.
func page(cursor *api.Cursor, limit *api.Limit) (core.Page, error) {
	p := core.Page{Limit: DefaultLimit}
	if limit != nil {
		p.Limit = min(max(int(*limit), 1), MaxLimit)
	}
	if cursor != nil && *cursor != "" {
		after, err := base64.RawURLEncoding.DecodeString(*cursor)
		if err != nil || len(after) == 0 {
			//nolint:wrapcheck // a problem, answered as it is
			return core.Page{}, problem.InvalidCursor.Error("The cursor belongs to no list of AWS integrations.")
		}
		p.After = string(after)
	}
	return p, nil
}

// pageOf is all as a page of views, with the cursor of the next page when
// more follows.
func (s Server) pageOf(ctx context.Context, acct kernel.AccountID, all []core.Integration, more bool) (api.AwsIntegrationPage, error) {
	views, err := s.views(ctx, acct, all...)
	if err != nil {
		return api.AwsIntegrationPage{}, err
	}
	out := api.AwsIntegrationPage{Items: views}
	if more && len(all) > 0 {
		next := base64.RawURLEncoding.EncodeToString([]byte(all[len(all)-1].Name))
		out.NextCursor = &next
	}
	return out, nil
}

// AwsIntegrations lists the integrations of the caller's account in the
// spaces it may read.
func (s Server) AwsIntegrations(ctx context.Context, req api.AwsIntegrationsRequestObject) (api.AwsIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	legacy := req.Params.IncludeLegacy != nil && *req.Params.IncludeLegacy
	all, more, err := s.svc.List(ctx, p, legacy, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.pageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrations200JSONResponse{Body: out}, nil
}

// AttachableAwsIntegrationsInSpace lists the integrations a stack in the
// space may attach that the caller may read.
func (s Server) AttachableAwsIntegrationsInSpace(
	ctx context.Context,
	req api.AttachableAwsIntegrationsInSpaceRequestObject,
) (api.AttachableAwsIntegrationsInSpaceResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	all, more, err := s.svc.Attachable(ctx, p, req.Params.Id, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.pageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.AttachableAwsIntegrationsInSpace200JSONResponse{Body: out}, nil
}

// StackAttachableAwsIntegrations lists the integrations the stack may
// attach that the caller may read.
func (s Server) StackAttachableAwsIntegrations(ctx context.Context, req api.StackAttachableAwsIntegrationsRequestObject) (api.StackAttachableAwsIntegrationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.Cursor, req.Params.Limit)
	if err != nil {
		return nil, err
	}
	all, more, err := s.svc.StackAttachable(ctx, p, req.StackId, pg)
	if err != nil {
		return nil, cloudError(err)
	}
	out, err := s.pageOf(ctx, p.AccountID, all, more)
	if err != nil {
		return nil, err
	}
	return api.StackAttachableAwsIntegrations200JSONResponse{Body: out}, nil
}

// AwsIntegration answers one integration of the caller's account.
func (s Server) AwsIntegration(ctx context.Context, req api.AwsIntegrationRequestObject) (api.AwsIntegrationResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.Get(ctx, p.AccountID, req.AwsIntegrationId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegration200JSONResponse{
		Body: v, Headers: api.AwsIntegration200ResponseHeaders{ETag: etag(i)},
	}, nil
}

// AwsIntegrationByName answers one integration of the caller's account by
// its name.
func (s Server) AwsIntegrationByName(ctx context.Context, req api.AwsIntegrationByNameRequestObject) (api.AwsIntegrationByNameResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.ByName(ctx, p, req.Params.Name)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationByName200JSONResponse{Body: v}, nil
}

// AwsIntegrationCreate creates an integration in the space its body
// names.
func (s Server) AwsIntegrationCreate(ctx context.Context, req api.AwsIntegrationCreateRequestObject) (api.AwsIntegrationCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	in := core.Input{
		Name: b.Name, Space: deref(b.Space), RoleARN: b.RoleArn, Region: b.Region,
		Duration: b.DurationSeconds, Worker: b.GenerateCredentialsInWorker,
		ExternalID: deref(b.ExternalID), Labels: b.Labels, Autoattach: deref(b.AutoattachEnabled),
		TagSession: deref(b.TagAssumeRole),
	}
	i, err := s.svc.Create(ctx, p, in)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationCreate201JSONResponse{Body: v}, nil
}

// updateInput is an update's body as an input.
func updateInput(b api.AwsIntegrationUpdateRequest) core.Input {
	return core.Input{
		Name: b.Name, Space: deref(b.Space), RoleARN: b.RoleArn, Region: b.Region,
		Duration: &b.DurationSeconds, Worker: b.GenerateCredentialsInWorker,
		ExternalID: b.ExternalID, Labels: b.Labels, Autoattach: deref(b.AutoattachEnabled),
		TagSession: deref(b.TagAssumeRole),
	}
}

// AwsIntegrationUpdate replaces an integration's settings with its body's.
func (s Server) AwsIntegrationUpdate(ctx context.Context, req api.AwsIntegrationUpdateRequestObject) (api.AwsIntegrationUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, err := s.svc.Update(ctx, p, req.AwsIntegrationId, strict.IfMatch(req.Params.IfMatch),
		updateInput(*req.Body))
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationUpdate200JSONResponse{
		Body: v, Headers: api.AwsIntegrationUpdate200ResponseHeaders{ETag: etag(i)},
	}, nil
}

// AwsIntegrationPatch changes the settings its merge patch names and
// keeps the rest, writing the result as AwsIntegrationUpdate writes its
// body.
func (s Server) AwsIntegrationPatch(ctx context.Context, req api.AwsIntegrationPatchRequestObject) (api.AwsIntegrationPatchResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.AwsIntegrationUpdateRequest, int64, error) {
		i, err := s.svc.Get(ctx, p.AccountID, req.AwsIntegrationId)
		if err != nil {
			return api.AwsIntegrationUpdateRequest{}, 0, err //nolint:wrapcheck // answered by cloudError
		}
		slugs, err := s.spaces.Slugs(ctx, p.AccountID)
		if err != nil {
			return api.AwsIntegrationUpdateRequest{}, 0, err //nolint:wrapcheck // the access domain names it
		}
		return inputView(i, slugs[i.Space]), i.Version, nil
	}
	write := func(body api.AwsIntegrationUpdateRequest, expect kernel.Expect) (core.Integration, error) {
		return s.svc.Update(ctx, p, req.AwsIntegrationId, expect, updateInput(body)) //nolint:wrapcheck // as above
	}
	i, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationPatch200JSONResponse{
		Body: v, Headers: api.AwsIntegrationPatch200ResponseHeaders{ETag: etag(i)},
	}, nil
}

// AwsIntegrationDelete deletes an integration and answers it as it was.
func (s Server) AwsIntegrationDelete(ctx context.Context, req api.AwsIntegrationDeleteRequestObject) (api.AwsIntegrationDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	// The view reads the integration's attachments, which the delete takes
	// with it, so it is read first.
	i, err := s.svc.Get(ctx, p.AccountID, req.AwsIntegrationId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.view(ctx, i)
	if err != nil {
		return nil, err
	}
	if _, err := s.svc.Delete(ctx, p, req.AwsIntegrationId,
		strict.IfMatch(req.Params.IfMatch)); err != nil {
		return nil, cloudError(err)
	}
	return api.AwsIntegrationDelete200JSONResponse{Body: v}, nil
}

// AwsIntegrationsEnabled answers whether the caller's account may use AWS
// integrations.
func (s Server) AwsIntegrationsEnabled(ctx context.Context, _ api.AwsIntegrationsEnabledRequestObject) (api.AwsIntegrationsEnabledResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := s.svc.Enabled(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the features domain names the read
	}
	return api.AwsIntegrationsEnabled200JSONResponse{Body: ok}, nil
}

// InstallationAwsAccountId answers the AWS account the installation
// assumes roles as.
//
//nolint:revive // the generated interface names it
func (s Server) InstallationAwsAccountId(ctx context.Context, _ api.InstallationAwsAccountIdRequestObject) (api.InstallationAwsAccountIdResponseObject, error) {
	account, err := s.svc.InstallationAccount(ctx)
	if err != nil {
		return nil, cloudError(err)
	}
	return api.InstallationAwsAccountId200JSONResponse{Body: account}, nil
}

// AwsIntegrationAttachedStack answers one of an integration's attachments.
func (s Server) AwsIntegrationAttachedStack(ctx context.Context, req api.AwsIntegrationAttachedStackRequestObject) (api.AwsIntegrationAttachedStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.svc.AttachedStack(ctx, p, req.AwsIntegrationId, req.Params.Id)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.attachment(ctx, a)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationAttachedStack200JSONResponse{Body: v}, nil
}

// AwsIntegrationExternalIdForStack answers the external ID and an example
// trust policy statement of an integration on a stack.
//
//nolint:revive // the generated interface names it
func (s Server) AwsIntegrationExternalIdForStack(
	ctx context.Context,
	req api.AwsIntegrationExternalIdForStackRequestObject,
) (api.AwsIntegrationExternalIdForStackResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	q := req.Params
	id, statement, err := s.svc.ExternalID(ctx, p, req.AwsIntegrationId, q.StackId, q.Read, q.Write)
	if err != nil {
		return nil, cloudError(err)
	}
	return api.AwsIntegrationExternalIdForStack200JSONResponse{Body: api.ExternalIdForStackResult{
		ExternalId: id, AssumeRolePolicyStatement: statement,
	}}, nil
}

// AwsIntegrationAttach attaches an integration to a stack.
func (s Server) AwsIntegrationAttach(ctx context.Context, req api.AwsIntegrationAttachRequestObject) (api.AwsIntegrationAttachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Attach(ctx, p, req.StackId, req.AwsIntegrationId, req.Body.Read,
		req.Body.Write)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.attachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationAttach200JSONResponse{Body: v}, nil
}

// AwsIntegrationAttachmentUpdate changes the sides an attachment serves.
func (s Server) AwsIntegrationAttachmentUpdate(ctx context.Context, req api.AwsIntegrationAttachmentUpdateRequestObject) (api.AwsIntegrationAttachmentUpdateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.UpdateAttachment(ctx, p, req.AttachmentId, req.Body.Read, req.Body.Write)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.attachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationAttachmentUpdate200JSONResponse{Body: v}, nil
}

// AwsIntegrationDetach detaches an integration from a stack and answers
// the attachment as it was.
func (s Server) AwsIntegrationDetach(ctx context.Context, req api.AwsIntegrationDetachRequestObject) (api.AwsIntegrationDetachResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	a, st, err := s.svc.Detach(ctx, p, req.AttachmentId)
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.attachmentOn(ctx, a, st)
	if err != nil {
		return nil, err
	}
	return api.AwsIntegrationDetach200JSONResponse{Body: v}, nil
}

// StackIntegrationAwsCreate sets a stack's legacy AWS role.
func (s Server) StackIntegrationAwsCreate(ctx context.Context, req api.StackIntegrationAwsCreateRequestObject) (api.StackIntegrationAwsCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	b := req.Body
	i, st, err := s.svc.SetLegacy(ctx, p, req.StackId, core.LegacyInput{
		RoleARN: b.RoleArn, Region: b.Region, Duration: b.DurationSeconds,
		Worker: deref(b.GenerateCredentialsInWorker), ExternalID: deref(b.ExternalID),
	})
	if err != nil {
		return nil, cloudError(err)
	}
	v, err := s.legacyView(ctx, i, st)
	if err != nil {
		return nil, err
	}
	return api.StackIntegrationAwsCreate200JSONResponse{Body: v}, nil
}

// StackIntegrationAwsDelete deletes a stack's legacy AWS role and answers
// it not activated.
func (s Server) StackIntegrationAwsDelete(ctx context.Context, req api.StackIntegrationAwsDeleteRequestObject) (api.StackIntegrationAwsDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	i, found, err := s.svc.DeleteLegacy(ctx, p, req.StackId)
	if err != nil {
		return nil, cloudError(err)
	}
	// The role is gone, so the answer is not activated; it names the role
	// it deleted, if any.
	var v api.StackIntegrationAws
	if found {
		v.AssumedRoleArn, v.DurationSeconds, v.Region = &i.RoleARN, &i.Duration, i.Region
		v.GenerateCredentialsInWorker = i.Worker
	}
	return api.StackIntegrationAwsDelete200JSONResponse{Body: v}, nil
}

func deref[T any](v *T) T {
	var zero T
	if v == nil {
		return zero
	}
	return *v
}

// cloudError answers the service's refusals. A *kernel.Invalid and a
// stale If-Match are answered as they are.
func cloudError(err error) error {
	var upstream *core.Upstream
	switch {
	case errors.Is(err, core.ErrUnsupported):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.InstallationUnsupported.Error(
			"The installation holds no AWS credentials, so it assumes no AWS role.")
	case errors.As(err, &upstream):
		//nolint:wrapcheck // as above
		return problem.UpstreamError.Error(fmt.Sprintf("AWS STS failed: %s.", upstream.Code))
	}
	if d, ok := errors.AsType[*core.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", d.Action))
		p.Action = d.Action
		return &problem.Error{Problem: p}
	}
	if m, ok := errors.AsType[*core.Missing](err); ok {
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No %s %q in this account.", m.Kind, m.ID))
	}
	if t, ok := errors.AsType[*core.NameTaken](err); ok {
		//nolint:wrapcheck // as above
		return problem.AlreadyExists.Error(
			fmt.Sprintf("Another AWS integration of this account is named %q.", t.Name))
	}
	if c, ok := errors.AsType[*core.Conflict](err); ok {
		//nolint:wrapcheck // as above
		return problem.InvalidState.Error(upper(c.Detail) + ".")
	}
	if errors.Is(err, core.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error("No such AWS integration in this account.")
	}
	return err
}

// upper is s with its first letter in upper case.
func upper(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
