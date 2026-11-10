package http

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
)

// apiKeysList names the API key list's cursors.
const apiKeysList = "api-keys"

// ApiKeys lists the API keys of the caller's account, oldest first.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeys(
	ctx context.Context,
	req api.ApiKeysRequestObject,
) (api.ApiKeysResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var after kernel.ULID
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](apiKeysList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if after, err = kernel.ParseULID(c.ID); err != nil {
			//nolint:wrapcheck // a problem, answered as it is
			return nil, problem.InvalidCursor.Error("The cursor's position is not an API key.")
		}
	}
	limit := int(kernel.PageLimit(req.Params.Limit))
	// One more than the page shows whether another follows.
	keys, err := s.keys.List(ctx, p, after, limit+1)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	acts, err := s.keyActions(ctx)
	if err != nil {
		return nil, err
	}
	body := api.ApiKeyPage{Items: make([]api.ApiKey, 0, min(len(keys), limit))}
	for _, k := range keys[:min(len(keys), limit)] {
		v, err := s.keyOf(ctx, k, "", acts)
		if err != nil {
			return nil, err
		}
		body.Items = append(body.Items, v)
	}
	if len(keys) > limit {
		last := keys[limit-1].ID.String()
		next, err := page.Encode(apiKeysList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return api.ApiKeys200JSONResponse{Body: body}, nil
}

// ApiKeyCreate creates an API key of the caller's account and answers it
// with its secret, the only time the secret is readable.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyCreate(
	ctx context.Context,
	req api.ApiKeyCreateRequestObject,
) (api.ApiKeyCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := apikey.Input{
		Name:        req.Body.Name,
		Description: deref(req.Body.Description),
		Teams:       req.Body.Teams,
		Machine:     deref(req.Body.Machine),
	}
	if req.Body.ExpiresAt != nil {
		in.ExpiresAt = time.Unix(int64(*req.Body.ExpiresAt), 0).UTC()
	}
	if o := req.Body.Oidc; o != nil {
		in.OIDC = &apikey.OIDC{
			Issuer:            o.Issuer,
			ClientID:          o.ClientId,
			SubjectExpression: o.SubjectExpression,
			ClaimMapping:      claimMapping(o.ClaimMappings),
		}
	}
	bs, err := s.prepare(ctx, p, binding.ActorAPIKey, legacy{
		rules:    ruleRequests(deref(req.Body.AccessRules)),
		bindings: roleRequests(deref(req.Body.RoleBindings)),
	})
	if err != nil {
		return nil, err
	}
	var (
		k      apikey.Key
		secret string
	)
	err = s.atomic(ctx, func(ctx context.Context) error {
		if k, secret, err = s.keys.Create(ctx, p, in); err != nil {
			return keyError(err, "")
		}
		return bindingError(s.bind(ctx, p.AccountID, binding.ActorAPIKey, k.ID, bs), "", "")
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // each write in the transaction answers its own problem
	}
	v, err := s.keyView(ctx, k, secret)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyCreate201JSONResponse{Body: v}, nil
}

// ApiKey reads an API key of the caller's account, tagged with its
// version; any other answers 404.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKey(
	ctx context.Context,
	req api.ApiKeyRequestObject,
) (api.ApiKeyResponseObject, error) {
	k, err := s.onKey(ctx, req.ApiKeyId, s.keys.Get)
	if err != nil {
		return nil, err
	}
	v, err := s.keyView(ctx, k, "")
	if err != nil {
		return nil, err
	}
	return api.ApiKey200JSONResponse{
		Body: v, Headers: api.ApiKey200ResponseHeaders{ETag: new(strict.ETag(k.Version))},
	}, nil
}

// ApiKeyUpdate changes the fields the body sets of an API key of the
// caller's account, when its If-Match, if any, names the key's version.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyUpdate(
	ctx context.Context,
	req api.ApiKeyUpdateRequestObject,
) (api.ApiKeyUpdateResponseObject, error) {
	k, err := s.updateKey(ctx, req.ApiKeyId, strict.IfMatch(req.Params.IfMatch), *req.Body)
	if err != nil {
		return nil, err
	}
	v, err := s.keyView(ctx, k, "")
	if err != nil {
		return nil, err
	}
	return api.ApiKeyUpdate200JSONResponse{
		Body: v, Headers: api.ApiKeyUpdate200ResponseHeaders{ETag: new(strict.ETag(k.Version))},
	}, nil
}

// ApiKeyPatch changes the fields its merge patch names and keeps the
// rest, writing the result as ApiKeyUpdate writes its body. A member the
// patch sets to null is kept, as ApiKeyUpdate keeps a member its body
// leaves out.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyPatch(
	ctx context.Context,
	req api.ApiKeyPatchRequestObject,
) (api.ApiKeyPatchResponseObject, error) {
	var patch map[string]any
	if req.Body != nil {
		patch = *req.Body
	}
	read := func() (api.ApiKeyUpdateInput, int64, error) {
		k, err := s.onKey(ctx, req.ApiKeyId, s.keys.Get)
		return keyInput(k), k.Version, err
	}
	write := func(in api.ApiKeyUpdateInput, expect kernel.Expect) (apikey.Key, error) {
		return s.updateKey(ctx, req.ApiKeyId, expect, in)
	}
	k, err := strict.Apply(strict.IfMatch(req.Params.IfMatch), patch, read, write)
	if err != nil {
		return nil, err //nolint:wrapcheck // read and write answer their problems
	}
	v, err := s.keyView(ctx, k, "")
	if err != nil {
		return nil, err
	}
	return api.ApiKeyPatch200JSONResponse{
		Body: v, Headers: api.ApiKeyPatch200ResponseHeaders{ETag: new(strict.ETag(k.Version))},
	}, nil
}

// updateKey applies in to the key raw names in the caller's account, as
// ApiKeyUpdate and ApiKeyPatch do, when expect accepts its version.
func (s Server) updateKey(
	ctx context.Context,
	raw string,
	expect kernel.Expect,
	in api.ApiKeyUpdateInput,
) (apikey.Key, error) {
	c := apikey.Change{Name: in.Name, Description: in.Description, Teams: in.Teams}
	if in.OidcClaimMappings != nil {
		m := claimMapping(in.OidcClaimMappings)
		c.ClaimMapping = &m
	}
	return s.onKey(ctx, raw,
		func(ctx context.Context, p kernel.Principal, id kernel.ULID) (apikey.Key, error) {
			return s.keys.Update(ctx, p, id, expect, c)
		})
}

// keyInput is k as the ApiKeyUpdateInput that writes it, with a claim
// mapping for an OIDC key alone, as no other key takes one.
func keyInput(k apikey.Key) api.ApiKeyUpdateInput {
	in := api.ApiKeyUpdateInput{
		Name: &k.Name, Description: &k.Description, Teams: new(nonNil(k.Teams)),
	}
	if k.Type == apikey.TypeOIDC {
		m := api.ClaimMappingInput{
			Entries: make([]api.ClaimMappingEntryInput, 0, len(k.OIDC.ClaimMapping)),
		}
		for _, e := range k.OIDC.ClaimMapping {
			m.Entries = append(m.Entries, api.ClaimMappingEntryInput{Name: e.Name, Value: e.Value})
		}
		in.OidcClaimMappings = &m
	}
	return in
}

// ApiKeyReset replaces the secret of an API key of the caller's account,
// revokes every session of the key, and answers the key with its new
// secret.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyReset(
	ctx context.Context,
	req api.ApiKeyResetRequestObject,
) (api.ApiKeyResetResponseObject, error) {
	var secret string
	k, err := s.onKey(ctx, req.ApiKeyId,
		func(ctx context.Context, p kernel.Principal, id kernel.ULID) (apikey.Key, error) {
			k, sec, err := s.keys.Reset(ctx, p, id)
			secret = sec
			return k, err //nolint:wrapcheck // onKey classifies it
		})
	if err != nil {
		return nil, err
	}
	v, err := s.keyView(ctx, k, secret)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyReset200JSONResponse{Body: v}, nil
}

// ApiKeyDelete deletes an API key of the caller's account, when its
// If-Match, if any, names the key's version, and revokes every session of
// the key.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyDelete(
	ctx context.Context,
	req api.ApiKeyDeleteRequestObject,
) (api.ApiKeyDeleteResponseObject, error) {
	expect := strict.IfMatch(req.Params.IfMatch)
	k, err := s.onKey(ctx, req.ApiKeyId,
		func(ctx context.Context, p kernel.Principal, id kernel.ULID) (apikey.Key, error) {
			return s.keys.Delete(ctx, p, id, expect)
		})
	if err != nil {
		return nil, err
	}
	v, err := s.keyView(ctx, k, "")
	if err != nil {
		return nil, err
	}
	return api.ApiKeyDelete200JSONResponse{Body: v}, nil
}

// ApiKeyEnableNonAdminManagers records whether admins of spaces below the
// account's top space may manage API keys, and answers the setting.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyEnableNonAdminManagers(
	ctx context.Context,
	req api.ApiKeyEnableNonAdminManagersRequestObject,
) (api.ApiKeyEnableNonAdminManagersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.keys.SetNonAdminManagers(ctx, p, req.Body.Enable)
	if err != nil {
		return nil, keyError(err, "")
	}
	return api.ApiKeyEnableNonAdminManagers200JSONResponse{Body: on}, nil
}

// onKey runs do on the key raw names in the caller's account. A key that
// is not a ULID, belongs to another account or is deleted answers 404, as
// hidden and missing are the same, so the API never reveals what exists.
//
// ref: decision 0022
func (s Server) onKey(
	ctx context.Context,
	raw string,
	do func(context.Context, kernel.Principal, kernel.ULID) (apikey.Key, error),
) (apikey.Key, error) {
	p, err := principal(ctx)
	if err != nil {
		return apikey.Key{}, err
	}
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return apikey.Key{}, keyError(port.ErrNotFound, raw)
	}
	k, err := do(ctx, p, id)
	if err != nil {
		return apikey.Key{}, keyError(err, raw)
	}
	return k, nil
}

// keyError is err as the problem it answers: 404 for a key the caller's
// account does not hold, 422 for input the service refuses, 403 for a key
// or a run managing keys where only people may, for a caller the
// managers' rule refuses, naming the action, and for a personal key that
// is not a member's own or that the account turns off, and err itself
// otherwise, which the store marks when the database is unreachable.
func keyError(err error, id string) error {
	if d, ok := errors.AsType[*apikey.Denied](err); ok {
		p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s on this account's API keys.",
			d.Action))
		p.Action = string(d.Action)
		return &problem.Error{Problem: p}
	}
	switch {
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.NotFound.Error(fmt.Sprintf("No API key %q in this account.", id))
	case errors.Is(err, apikey.ErrInvalid):
		return problem.ValidationFailed.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, apikey.ErrNonHuman), errors.Is(err, apikey.ErrNoOwner),
		errors.Is(err, apikey.ErrPersonalOff):
		return problem.Forbidden.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

// keyActions are the actions the caller may perform on a key of its
// account, by the apikey service's rule, which names no key.
func (s Server) keyActions(ctx context.Context) ([]api.Action, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	acts, err := s.keys.Actions(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	return actions(acts), nil
}

// keyView is keyOf k with the caller's keyActions.
func (s Server) keyView(ctx context.Context, k apikey.Key, secret string) (api.ApiKey, error) {
	acts, err := s.keyActions(ctx)
	if err != nil {
		return api.ApiKey{}, err
	}
	return s.keyOf(ctx, k, secret, acts)
}

// keyOf is apiKey of k with acts and its bindings.
func (s Server) keyOf(
	ctx context.Context,
	k apikey.Key,
	secret string,
	acts []api.Action,
) (api.ApiKey, error) {
	held, err := s.bindings.Held(ctx, k.AccountID, binding.ActorAPIKey, k.ID)
	if err != nil {
		return api.ApiKey{}, err //nolint:wrapcheck // the store names the read
	}
	return apiKey(k, secret, acts, held), nil
}

// apiKey is k in the reference's ApiKey shape, with secret when create or
// reset has just made one, and acts as its allowedActions. held are its
// bindings: admin reads whether one is space-admin on the account's top
// space, whose ID is the account's, accessRules are those its access
// rules made, and spaceCount the spaces they name.
func apiKey(k apikey.Key, secret string, acts []api.Action, held []binding.Listed) api.ApiKey {
	out := api.ApiKey{
		Admin:          topAdmin(held, k.AccountID),
		AllowedActions: acts,
		AccessRules:    rules(held),
		SpaceCount:     spaceCount(held),
		CreatedAt:      unix(k.CreatedAt),
		Creator:        k.Creator,
		Deleted:        !k.DeletedAt.IsZero(),
		Description:    k.Description,
		Disabled:       !k.DisabledAt.IsZero(),
		Id:             k.ID.String(),
		IsMachineUser:  k.Machine,
		IsPersonal:     k.Personal(),
		Name:           k.Name,
		TeamCount:      int32(min(len(k.Teams), 1<<31-1)), //nolint:gosec // clamped
		Teams:          nonNil(k.Teams),
		Type:           api.APIKeyType(k.Type),
		DisabledAt:     optUnix(k.DisabledAt),
		ExpiresAt:      optUnix(k.ExpiresAt),
		LastUsedAt:     optUnix(k.LastUsedAt),
	}
	if secret != "" {
		out.Secret = &secret
	}
	if k.Personal() {
		out.Owner = &k.OwnerLogin
	}
	if k.DisabledReason != "" {
		r := api.ApiKeyDisabledReason(k.DisabledReason)
		out.DisabledReason = &r
	}
	if k.Type == apikey.TypeOIDC {
		entries := make([]api.ClaimMappingEntry, 0, len(k.OIDC.ClaimMapping))
		for _, m := range k.OIDC.ClaimMapping {
			entries = append(entries, api.ClaimMappingEntry{Name: m.Name, Value: m.Value})
		}
		out.OidcSettings = &api.ApiKeyOIDCSettings{
			ClaimMapping:      api.ClaimMapping{Entries: entries},
			ClientId:          k.OIDC.ClientID,
			Issuer:            k.OIDC.Issuer,
			SubjectExpression: k.OIDC.SubjectExpression,
		}
	}
	return out
}

// claimMapping is in as the core's mapping; nil is none.
func claimMapping(in *api.ClaimMappingInput) []apikey.Mapping {
	if in == nil {
		return nil
	}
	out := make([]apikey.Mapping, 0, len(in.Entries))
	for _, e := range in.Entries {
		out = append(out, apikey.Mapping{Name: e.Name, Value: e.Value})
	}
	return out
}

// optUnix is t in Unix seconds, or nil for the zero time.
func optUnix(t time.Time) *int32 {
	if t.IsZero() {
		return nil
	}
	u := unix(t)
	return &u
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
