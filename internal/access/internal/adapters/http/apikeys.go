package http

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
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
	body := api.ApiKeyPage{Items: make([]api.ApiKey, 0, min(len(keys), limit))}
	for _, k := range keys[:min(len(keys), limit)] {
		body.Items = append(body.Items, apiKey(k, ""))
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
	k, secret, err := s.keys.Create(ctx, p, in)
	if err != nil {
		return nil, keyError(err, "")
	}
	return api.ApiKeyCreate201JSONResponse{Body: apiKey(k, secret)}, nil
}

// ApiKey reads an API key of the caller's account; any other answers 404.
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
	return api.ApiKey200JSONResponse{Body: apiKey(k, "")}, nil
}

// ApiKeyUpdate changes the fields the body sets of an API key of the
// caller's account.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyUpdate(
	ctx context.Context,
	req api.ApiKeyUpdateRequestObject,
) (api.ApiKeyUpdateResponseObject, error) {
	c := apikey.Change{
		Name:        req.Body.Name,
		Description: req.Body.Description,
		Teams:       req.Body.Teams,
	}
	if req.Body.OidcClaimMappings != nil {
		m := claimMapping(req.Body.OidcClaimMappings)
		c.ClaimMapping = &m
	}
	k, err := s.onKey(ctx, req.ApiKeyId,
		func(ctx context.Context, p kernel.Principal, id kernel.ULID) (apikey.Key, error) {
			return s.keys.Update(ctx, p, id, c)
		})
	if err != nil {
		return nil, err
	}
	return api.ApiKeyUpdate200JSONResponse{Body: apiKey(k, "")}, nil
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
	return api.ApiKeyReset200JSONResponse{Body: apiKey(k, secret)}, nil
}

// ApiKeyDelete deletes an API key of the caller's account and revokes
// every session of the key.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeyDelete(
	ctx context.Context,
	req api.ApiKeyDeleteRequestObject,
) (api.ApiKeyDeleteResponseObject, error) {
	k, err := s.onKey(ctx, req.ApiKeyId, s.keys.Delete)
	if err != nil {
		return nil, err
	}
	return api.ApiKeyDelete200JSONResponse{Body: apiKey(k, "")}, nil
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
// hidden and missing are the same (decision 0022).
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
// or a run managing keys where only people may, and for a personal key
// that is not a member's own or that the account turns off, and err
// itself otherwise, which the store marks when the database is
// unreachable.
func keyError(err error, id string) error {
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

// apiKey is k in the reference's ApiKey shape, with secret when create or
// reset has just made one. admin reads whether the key holds a space-admin
// binding on the account's top space, which no key can until API key
// bindings are served, and access rules and spaces are bindings too.
func apiKey(k apikey.Key, secret string) api.ApiKey {
	out := api.ApiKey{
		AccessRules:   []api.SpaceAccessRule{},
		CreatedAt:     unix(k.CreatedAt),
		Creator:       k.Creator,
		Deleted:       !k.DeletedAt.IsZero(),
		Description:   k.Description,
		Disabled:      !k.DisabledAt.IsZero(),
		Id:            k.ID.String(),
		IsMachineUser: k.Machine,
		IsPersonal:    k.Personal(),
		Name:          k.Name,
		TeamCount:     int32(min(len(k.Teams), 1<<31-1)), //nolint:gosec // clamped
		Teams:         nonNil(k.Teams),
		Type:          api.APIKeyType(k.Type),
		DisabledAt:    optUnix(k.DisabledAt),
		ExpiresAt:     optUnix(k.ExpiresAt),
		LastUsedAt:    optUnix(k.LastUsedAt),
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
