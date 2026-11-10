package http

import (
	"context"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

// personalAPIKeysAccountList names the account's personal key list's
// cursors.
const personalAPIKeysAccountList = "personal-api-keys"

// PersonalApiKeys lists the personal API keys of the caller's account,
// oldest first.
//
//nolint:revive // the generated interface names it
func (s Server) PersonalApiKeys(
	ctx context.Context,
	req api.PersonalApiKeysRequestObject,
) (api.PersonalApiKeysResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	keys, next, err := listPage(personalAPIKeysAccountList, req.Params.Cursor, req.Params.Limit,
		parseULID, func(after kernel.ULID, n int) ([]apikey.Key, error) {
			return s.keys.Personal(ctx, p, after, n)
		}, func(k apikey.Key) string { return k.ID.String() })
	if err != nil {
		return nil, err
	}
	acts, err := s.keyActions(ctx)
	if err != nil {
		return nil, err
	}
	body := api.ApiKeyPage{Items: make([]api.ApiKey, 0, len(keys)), NextCursor: next}
	for _, k := range keys {
		v, err := s.keyOf(ctx, k, "", acts)
		if err != nil {
			return nil, err
		}
		body.Items = append(body.Items, v)
	}
	return api.PersonalApiKeys200JSONResponse{Body: body}, nil
}

// PersonalApiKeyCreate creates a personal API key owned by the calling
// member and answers it with its secret, the only time the secret is
// readable.
//
//nolint:revive // the generated interface names it
func (s Server) PersonalApiKeyCreate(
	ctx context.Context,
	req api.PersonalApiKeyCreateRequestObject,
) (api.PersonalApiKeyCreateResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	in := apikey.PersonalInput{Name: req.Body.Name, Description: deref(req.Body.Description)}
	if req.Body.ExpiresAt != nil {
		in.ExpiresAt = time.Unix(int64(*req.Body.ExpiresAt), 0).UTC()
	}
	k, secret, err := s.keys.CreatePersonal(ctx, p, in)
	if err != nil {
		return nil, keyError(err, "")
	}
	v, err := s.keyView(ctx, k, secret)
	if err != nil {
		return nil, err
	}
	return api.PersonalApiKeyCreate201JSONResponse{Body: v}, nil
}

// PersonalApiKeysCount counts the personal API keys of the caller's
// account that still work.
//
//nolint:revive // the generated interface names it
func (s Server) PersonalApiKeysCount(
	ctx context.Context,
	_ api.PersonalApiKeysCountRequestObject,
) (api.PersonalApiKeysCountResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.keys.PersonalCount(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	//nolint:gosec // clamped
	return api.PersonalApiKeysCount200JSONResponse{Body: int32(min(n, 1<<31-1))}, nil
}

// PersonalApiKeyDisable disables a personal API key of the caller's
// account and revokes every session of it; a key of the account's own
// answers 404.
//
//nolint:revive // the generated interface names it
func (s Server) PersonalApiKeyDisable(
	ctx context.Context,
	req api.PersonalApiKeyDisableRequestObject,
) (api.PersonalApiKeyDisableResponseObject, error) {
	k, err := s.onKey(ctx, req.PersonalApiKeyId, s.keys.DisablePersonal)
	if err != nil {
		return nil, err
	}
	v, err := s.keyView(ctx, k, "")
	if err != nil {
		return nil, err
	}
	return api.PersonalApiKeyDisable200JSONResponse{Body: v}, nil
}
