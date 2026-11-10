package http

import (
	"context"
	"errors"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// bindingKind is how one actor kind's binding operations read their
// input I and answer their view V.
type bindingKind[I, V any] struct {
	actor  binding.ActorKind
	what   string
	fields binding.Fields
	req    func(I) binding.Request
	input  func(binding.Listed) I
	view   func(binding.Listed) V
}

var (
	userBindings = bindingKind[api.UserRoleBindingInput, api.UserRoleBinding]{
		actor: binding.ActorUser, what: "managed user",
		fields: binding.Fields{Actor: "userID", Role: "roleID", Space: "spaceID"},
		req: func(in api.UserRoleBindingInput) binding.Request {
			return binding.Request{Actor: in.UserID, Role: in.RoleID, Space: in.SpaceID}
		},
		input: func(l binding.Listed) api.UserRoleBindingInput {
			return api.UserRoleBindingInput{
				UserID:  l.ActorID.String(),
				RoleID:  l.Role,
				SpaceID: l.Space,
			}
		},
		view: userBinding,
	}
	groupBindings = bindingKind[api.UserGroupRoleBindingInput, api.UserGroupRoleBinding]{
		actor: binding.ActorGroup, what: "managed user group",
		fields: binding.Fields{Actor: "userGroupID", Role: "roleID", Space: "spaceID"},
		req: func(in api.UserGroupRoleBindingInput) binding.Request {
			return binding.Request{Actor: in.UserGroupID, Role: in.RoleID, Space: in.SpaceID}
		},
		input: func(l binding.Listed) api.UserGroupRoleBindingInput {
			return api.UserGroupRoleBindingInput{
				UserGroupID: l.ActorID.String(), RoleID: l.Role, SpaceID: l.Space,
			}
		},
		view: groupBinding,
	}
	keyBindings = bindingKind[api.ApiKeyRoleBindingInput, api.ApiKeyRoleBinding]{
		actor: binding.ActorAPIKey, what: "API key",
		fields: binding.Fields{Actor: "apiKeyID", Role: "roleID", Space: "spaceID"},
		req: func(in api.ApiKeyRoleBindingInput) binding.Request {
			return binding.Request{Actor: in.ApiKeyID, Role: in.RoleID, Space: in.SpaceID}
		},
		input: func(l binding.Listed) api.ApiKeyRoleBindingInput {
			return api.ApiKeyRoleBindingInput{
				ApiKeyID: l.ActorID.String(),
				RoleID:   l.Role,
				SpaceID:  l.Space,
			}
		},
		view: keyBinding,
	}
)

func userBinding(l binding.Listed) api.UserRoleBinding {
	id := l.ActorID.String()
	return api.UserRoleBinding{
		Id: l.ID.String(), Kind: api.UserRoleBindingKindUserRoleBinding,
		Role: api.RoleRef{Id: l.Role}, RoleID: l.Role, Space: api.SpaceRef{Id: l.Space},
		User: api.ManagedUserRef{Id: id}, UserID: id,
	}
}

func groupBinding(l binding.Listed) api.UserGroupRoleBinding {
	return api.UserGroupRoleBinding{
		Id: l.ID.String(), Kind: api.UserGroupRoleBindingKindUserGroupRoleBinding,
		RoleID: l.Role, Space: api.SpaceRef{Id: l.Space},
		UserGroup: api.ManagedUserGroupRef{Id: l.ActorID.String()},
	}
}

func keyBinding(l binding.Listed) api.ApiKeyRoleBinding {
	id := l.ActorID.String()
	return api.ApiKeyRoleBinding{
		Id: l.ID.String(), Kind: api.ApiKeyRoleBindingKindApiKeyRoleBinding,
		Role: api.RoleRef{Id: l.Role}, Space: api.SpaceRef{Id: l.Space},
		ApiKey: api.ApiKeyRef{Id: id}, ApiKeyID: id,
	}
}

func views[I, V any](k bindingKind[I, V], ls []binding.Listed) []V {
	out := make([]V, 0, len(ls))
	for _, l := range ls {
		out = append(out, k.view(l))
	}
	return out
}

// readBinding answers the binding id of k's actors in the caller's
// account, and its entity tag.
func readBinding[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	id string,
) (V, *string, error) {
	var zero V
	p, err := principal(ctx)
	if err != nil {
		return zero, nil, err
	}
	l, err := s.bindings.Binding(ctx, p.AccountID, k.actor, id)
	if err != nil {
		return zero, nil, bindingError(err, "binding", "")
	}
	return k.view(l), new(strict.ETag(l.Version)), nil
}

// createBindings records ins, the bindings an input names at pointer,
// all or none; an empty pointer names an input of one.
func createBindings[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	pointer string,
	ins []I,
) ([]V, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	reqs := make([]binding.Request, 0, len(ins))
	for _, in := range ins {
		reqs = append(reqs, k.req(in))
	}
	ls, err := s.bindings.Create(ctx, p, k.actor, pointer, k.fields, reqs)
	if err != nil {
		return nil, bindingError(err, "binding", pointer)
	}
	return views(k, ls), nil
}

// updateBinding moves the binding id of k's actors to in's role and
// space, when expect accepts its version.
func updateBinding[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	id string,
	expect kernel.Expect,
	in I,
) (V, *string, error) {
	var zero V
	p, err := principal(ctx)
	if err != nil {
		return zero, nil, err
	}
	l, err := s.bindings.Update(ctx, p, k.actor, id, expect, k.fields, k.req(in))
	if err != nil {
		return zero, nil, bindingError(err, "binding", "")
	}
	return k.view(l), new(strict.ETag(l.Version)), nil
}

// patchBinding merges patch into the binding id's input and writes the
// result as updateBinding does.
func patchBinding[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	id string,
	ifMatch *string,
	body *api.MergePatch,
) (V, *string, error) {
	var zero V
	p, err := principal(ctx)
	if err != nil {
		return zero, nil, err
	}
	var patch map[string]any
	if body != nil {
		patch = *body
	}
	read := func() (I, int64, error) {
		l, err := s.bindings.Binding(ctx, p.AccountID, k.actor, id)
		if err != nil {
			var none I
			return none, 0, bindingError(err, "binding", "")
		}
		return k.input(l), l.Version, nil
	}
	type written struct {
		v   V
		tag *string
	}
	write := func(in I, expect kernel.Expect) (written, error) {
		v, tag, err := updateBinding(ctx, s, k, id, expect, in)
		return written{v, tag}, err
	}
	w, err := strict.Apply(strict.IfMatch(ifMatch), patch, read, write)
	if err != nil {
		return zero, nil, err //nolint:wrapcheck // read and write answer their problems
	}
	return w.v, w.tag, nil
}

// deleteBinding deletes the binding id of k's actors, when its If-Match,
// if any, names its version, and answers it as it was.
func deleteBinding[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	id string,
	ifMatch *string,
) (V, error) {
	var zero V
	p, err := principal(ctx)
	if err != nil {
		return zero, err
	}
	l, err := s.bindings.Delete(ctx, p.AccountID, k.actor, id, strict.IfMatch(ifMatch))
	if err != nil {
		return zero, bindingError(err, "binding", "")
	}
	return k.view(l), nil
}

// actorBindings answers the bindings of the actor of k's kind id names.
func actorBindings[I, V any](
	ctx context.Context,
	s Server,
	k bindingKind[I, V],
	id string,
) ([]V, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	ls, err := s.bindings.Of(ctx, p.AccountID, k.actor, id)
	if err != nil {
		return nil, bindingError(err, k.what, "")
	}
	return views(k, ls), nil
}

// bindingError is err as the problem it answers: 404 for a binding or
// actor the account lacks, 409 already-exists for a binding the account
// holds, naming its element of a batch at pointer, and err itself
// otherwise, a *kernel.Invalid answering 422 with its pointers.
func bindingError(err error, what, pointer string) error {
	if d, ok := errors.AsType[*binding.Duplicate](err); ok {
		at := ""
		if pointer != "" {
			at = fmt.Sprintf(" at %s/%d", pointer, d.Index)
		}
		//nolint:wrapcheck // a problem, answered as it is
		return problem.AlreadyExists.Error(fmt.Sprintf(
			"The binding%s is one the account holds: the actor already holds the role in the space.",
			at,
		))
	}
	if errors.Is(err, port.ErrNotFound) {
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No such %s in this account.", what))
	}
	return err
}

// rules are the access rules among held, an actor's bindings: each binding
// an access rule made, as the level its system role grants.
func rules(held []binding.Listed) []api.SpaceAccessRule {
	out := []api.SpaceAccessRule{}
	for _, l := range held {
		if level, ok := role.LevelOf(l.Role); ok && l.Origin == binding.OriginAccessRule {
			out = append(
				out,
				api.SpaceAccessRule{Space: l.Space, SpaceAccessLevel: api.SpaceAccessLevel(level)},
			)
		}
	}
	return out
}

// ruleInputs are rules as the input that writes them.
func ruleInputs(rs []api.SpaceAccessRule) []api.SpaceAccessRuleInput {
	out := make([]api.SpaceAccessRuleInput, 0, len(rs))
	for _, r := range rs {
		out = append(out, api.SpaceAccessRuleInput(r))
	}
	return out
}

// spaceCount is the number of spaces held, an actor's bindings, name.
func spaceCount(held []binding.Listed) int32 {
	spaces := map[kernel.ULID]bool{}
	for _, l := range held {
		spaces[l.SpaceID] = true
	}
	return int32(min(len(spaces), 1<<31-1)) //nolint:gosec // clamped
}

// topAdmin reports whether held, an actor's bindings, binds it
// space-admin on acct's top space, whose ID is the account's.
func topAdmin(held []binding.Listed, acct kernel.AccountID) bool {
	for _, l := range held {
		if l.Role == role.SpaceAdmin && l.SpaceID == acct.ULID {
			return true
		}
	}
	return false
}

// legacy is the bindings an actor's input carries: its access rules, each
// a binding of the system role its level names, a level of NONE granting
// none, and its role bindings.
type legacy struct {
	rules    []binding.Request
	bindings []binding.Request
}

func ruleRequests(rules []api.SpaceAccessRuleInput) []binding.Request {
	out := make([]binding.Request, 0, len(rules))
	for _, r := range rules {
		if slug, ok := role.ForLevel(string(r.SpaceAccessLevel)); ok {
			out = append(out, binding.Request{Role: slug, Space: r.Space})
		}
	}
	return out
}

func roleRequests(bs []api.RoleBindingInput) []binding.Request {
	out := make([]binding.Request, 0, len(bs))
	for _, b := range bs {
		out = append(out, binding.Request{Role: b.RoleID, Space: b.SpaceID})
	}
	return out
}

// prepared are the bindings of an actor's input, checked before the
// actor is written.
type prepared struct {
	rules, bindings []binding.Binding
}

// prepare checks in's bindings of an actor of kind as p's, refusing each
// at /accessRules/<index> or /roleBindings/<index>.
func (s Server) prepare(
	ctx context.Context,
	p kernel.Principal,
	kind binding.ActorKind,
	in legacy,
) (prepared, error) {
	var out prepared
	var errs []error
	var err error
	if len(in.rules) > 0 {
		out.rules, err = s.bindings.Prepare(ctx, p, kind, binding.OriginAccessRule, "/accessRules",
			binding.Fields{Space: "space"}, in.rules)
		errs = append(errs, err)
	}
	if len(in.bindings) > 0 {
		out.bindings, err = s.bindings.Prepare(ctx, p, kind, binding.OriginBinding, "/roleBindings",
			binding.Fields{Role: "roleID", Space: "spaceID"}, in.bindings)
		errs = append(errs, err)
	}
	return out, joinInvalid(errs)
}

// joinInvalid is the first error of errs that is no refusal, or one
// refusal holding every refusal's field errors.
func joinInvalid(errs []error) error {
	var all *kernel.Invalid
	for _, err := range errs {
		if err == nil {
			continue
		}
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok {
			return err
		}
		if all == nil {
			all = &kernel.Invalid{Detail: "The bindings are refused."}
		}
		all.Errors = append(all.Errors, inv.Errors...)
	}
	if all == nil {
		return nil
	}
	return all
}

// bind records b's bindings for the actor of kind id, in the transaction
// ctx carries.
func (s Server) bind(
	ctx context.Context,
	acct kernel.AccountID,
	kind binding.ActorKind,
	id kernel.ULID,
	b prepared,
) error {
	_, err := s.bindings.Bind(ctx, acct, kind, id, append(b.rules, b.bindings...))
	return err //nolint:wrapcheck // the store names the write
}

// UserRoleBinding answers one of the account's user role bindings.
func (s Server) UserRoleBinding(
	ctx context.Context,
	req api.UserRoleBindingRequestObject,
) (api.UserRoleBindingResponseObject, error) {
	v, tag, err := readBinding(ctx, s, userBindings, req.UserRoleBindingId)
	if err != nil {
		return nil, err
	}
	return api.UserRoleBinding200JSONResponse{
		Body:    v,
		Headers: api.UserRoleBinding200ResponseHeaders{ETag: tag},
	}, nil
}

// UserRoleBindingCreate binds a managed user to a role in a space.
func (s Server) UserRoleBindingCreate(
	ctx context.Context,
	req api.UserRoleBindingCreateRequestObject,
) (api.UserRoleBindingCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, userBindings, "", []api.UserRoleBindingInput{*req.Body})
	if err != nil {
		return nil, err
	}
	return api.UserRoleBindingCreate201JSONResponse{Body: vs[0]}, nil
}

// UserRoleBindingBatchCreate binds managed users to roles in spaces, all
// or none.
func (s Server) UserRoleBindingBatchCreate(
	ctx context.Context,
	req api.UserRoleBindingBatchCreateRequestObject,
) (api.UserRoleBindingBatchCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, userBindings, "/bindings", req.Body.Bindings)
	if err != nil {
		return nil, err
	}
	return api.UserRoleBindingBatchCreate201JSONResponse{Body: vs}, nil
}

// UserRoleBindingUpdate moves a user role binding to another role or
// space.
func (s Server) UserRoleBindingUpdate(
	ctx context.Context,
	req api.UserRoleBindingUpdateRequestObject,
) (api.UserRoleBindingUpdateResponseObject, error) {
	v, tag, err := updateBinding(ctx, s, userBindings, req.UserRoleBindingId,
		strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, err
	}
	return api.UserRoleBindingUpdate200JSONResponse{
		Body: v, Headers: api.UserRoleBindingUpdate200ResponseHeaders{ETag: tag},
	}, nil
}

// UserRoleBindingPatch merges a patch into a user role binding.
func (s Server) UserRoleBindingPatch(
	ctx context.Context,
	req api.UserRoleBindingPatchRequestObject,
) (api.UserRoleBindingPatchResponseObject, error) {
	v, tag, err := patchBinding(ctx, s, userBindings, req.UserRoleBindingId, req.Params.IfMatch,
		req.Body)
	if err != nil {
		return nil, err
	}
	return api.UserRoleBindingPatch200JSONResponse{
		Body: v, Headers: api.UserRoleBindingPatch200ResponseHeaders{ETag: tag},
	}, nil
}

// UserRoleBindingDelete deletes a user role binding.
func (s Server) UserRoleBindingDelete(
	ctx context.Context,
	req api.UserRoleBindingDeleteRequestObject,
) (api.UserRoleBindingDeleteResponseObject, error) {
	v, err := deleteBinding(ctx, s, userBindings, req.UserRoleBindingId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return api.UserRoleBindingDelete200JSONResponse{Body: v}, nil
}

// ManagedUserUserRoleBindings lists a managed user's bindings.
func (s Server) ManagedUserUserRoleBindings(
	ctx context.Context,
	req api.ManagedUserUserRoleBindingsRequestObject,
) (api.ManagedUserUserRoleBindingsResponseObject, error) {
	vs, err := actorBindings(ctx, s, userBindings, req.ManagedUserId)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserUserRoleBindings200JSONResponse{Body: vs}, nil
}

// UserGroupRoleBinding answers one of the account's group role bindings.
func (s Server) UserGroupRoleBinding(
	ctx context.Context,
	req api.UserGroupRoleBindingRequestObject,
) (api.UserGroupRoleBindingResponseObject, error) {
	v, tag, err := readBinding(ctx, s, groupBindings, req.UserGroupRoleBindingId)
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBinding200JSONResponse{
		Body: v, Headers: api.UserGroupRoleBinding200ResponseHeaders{ETag: tag},
	}, nil
}

// UserGroupRoleBindingCreate binds a managed user group to a role in a
// space.
func (s Server) UserGroupRoleBindingCreate(
	ctx context.Context,
	req api.UserGroupRoleBindingCreateRequestObject,
) (api.UserGroupRoleBindingCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, groupBindings, "", []api.UserGroupRoleBindingInput{*req.Body})
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBindingCreate201JSONResponse{Body: vs[0]}, nil
}

// UserGroupRoleBindingBatchCreate binds groups to roles in spaces, all or
// none.
func (s Server) UserGroupRoleBindingBatchCreate(
	ctx context.Context,
	req api.UserGroupRoleBindingBatchCreateRequestObject,
) (api.UserGroupRoleBindingBatchCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, groupBindings, "/bindings", req.Body.Bindings)
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBindingBatchCreate201JSONResponse{Body: vs}, nil
}

// UserGroupRoleBindingUpdate moves a group role binding to another role
// or space.
func (s Server) UserGroupRoleBindingUpdate(
	ctx context.Context,
	req api.UserGroupRoleBindingUpdateRequestObject,
) (api.UserGroupRoleBindingUpdateResponseObject, error) {
	v, tag, err := updateBinding(ctx, s, groupBindings, req.UserGroupRoleBindingId,
		strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBindingUpdate200JSONResponse{
		Body: v, Headers: api.UserGroupRoleBindingUpdate200ResponseHeaders{ETag: tag},
	}, nil
}

// UserGroupRoleBindingPatch merges a patch into a group role binding.
func (s Server) UserGroupRoleBindingPatch(
	ctx context.Context,
	req api.UserGroupRoleBindingPatchRequestObject,
) (api.UserGroupRoleBindingPatchResponseObject, error) {
	v, tag, err := patchBinding(ctx, s, groupBindings, req.UserGroupRoleBindingId,
		req.Params.IfMatch, req.Body)
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBindingPatch200JSONResponse{
		Body: v, Headers: api.UserGroupRoleBindingPatch200ResponseHeaders{ETag: tag},
	}, nil
}

// UserGroupRoleBindingDelete deletes a group role binding.
func (s Server) UserGroupRoleBindingDelete(
	ctx context.Context,
	req api.UserGroupRoleBindingDeleteRequestObject,
) (api.UserGroupRoleBindingDeleteResponseObject, error) {
	v, err := deleteBinding(ctx, s, groupBindings, req.UserGroupRoleBindingId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return api.UserGroupRoleBindingDelete200JSONResponse{Body: v}, nil
}

// ManagedUserGroupUserRoleBindings lists a managed user group's bindings.
func (s Server) ManagedUserGroupUserRoleBindings(
	ctx context.Context,
	req api.ManagedUserGroupUserRoleBindingsRequestObject,
) (api.ManagedUserGroupUserRoleBindingsResponseObject, error) {
	vs, err := actorBindings(ctx, s, groupBindings, req.ManagedUserGroupId)
	if err != nil {
		return nil, err
	}
	return api.ManagedUserGroupUserRoleBindings200JSONResponse{Body: vs}, nil
}

// ApiKeyRoleBinding answers one of the account's API key role bindings.
func (s Server) ApiKeyRoleBinding(
	ctx context.Context,
	req api.ApiKeyRoleBindingRequestObject,
) (api.ApiKeyRoleBindingResponseObject, error) {
	v, tag, err := readBinding(ctx, s, keyBindings, req.ApiKeyRoleBindingId)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBinding200JSONResponse{
		Body: v, Headers: api.ApiKeyRoleBinding200ResponseHeaders{ETag: tag},
	}, nil
}

// ApiKeyRoleBindingCreate binds an API key to a role in a space.
func (s Server) ApiKeyRoleBindingCreate(
	ctx context.Context,
	req api.ApiKeyRoleBindingCreateRequestObject,
) (api.ApiKeyRoleBindingCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, keyBindings, "", []api.ApiKeyRoleBindingInput{*req.Body})
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBindingCreate201JSONResponse{Body: vs[0]}, nil
}

// ApiKeyRoleBindingBatchCreate binds API keys to roles in spaces, all or
// none.
func (s Server) ApiKeyRoleBindingBatchCreate(
	ctx context.Context,
	req api.ApiKeyRoleBindingBatchCreateRequestObject,
) (api.ApiKeyRoleBindingBatchCreateResponseObject, error) {
	vs, err := createBindings(ctx, s, keyBindings, "/bindings", req.Body.Bindings)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBindingBatchCreate201JSONResponse{Body: vs}, nil
}

// ApiKeyRoleBindingUpdate moves an API key role binding to another role
// or space.
func (s Server) ApiKeyRoleBindingUpdate(
	ctx context.Context,
	req api.ApiKeyRoleBindingUpdateRequestObject,
) (api.ApiKeyRoleBindingUpdateResponseObject, error) {
	v, tag, err := updateBinding(ctx, s, keyBindings, req.ApiKeyRoleBindingId,
		strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBindingUpdate200JSONResponse{
		Body: v, Headers: api.ApiKeyRoleBindingUpdate200ResponseHeaders{ETag: tag},
	}, nil
}

// ApiKeyRoleBindingPatch merges a patch into an API key role binding.
func (s Server) ApiKeyRoleBindingPatch(
	ctx context.Context,
	req api.ApiKeyRoleBindingPatchRequestObject,
) (api.ApiKeyRoleBindingPatchResponseObject, error) {
	v, tag, err := patchBinding(ctx, s, keyBindings, req.ApiKeyRoleBindingId, req.Params.IfMatch,
		req.Body)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBindingPatch200JSONResponse{
		Body: v, Headers: api.ApiKeyRoleBindingPatch200ResponseHeaders{ETag: tag},
	}, nil
}

// ApiKeyRoleBindingDelete deletes an API key role binding.
func (s Server) ApiKeyRoleBindingDelete(
	ctx context.Context,
	req api.ApiKeyRoleBindingDeleteRequestObject,
) (api.ApiKeyRoleBindingDeleteResponseObject, error) {
	v, err := deleteBinding(ctx, s, keyBindings, req.ApiKeyRoleBindingId, req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyRoleBindingDelete200JSONResponse{Body: v}, nil
}

// ApiKeyApiKeyRoleBindings lists an API key's bindings.
func (s Server) ApiKeyApiKeyRoleBindings(
	ctx context.Context,
	req api.ApiKeyApiKeyRoleBindingsRequestObject,
) (api.ApiKeyApiKeyRoleBindingsResponseObject, error) {
	vs, err := actorBindings(ctx, s, keyBindings, req.ApiKeyId)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyApiKeyRoleBindings200JSONResponse{Body: vs}, nil
}
