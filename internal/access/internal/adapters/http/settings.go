package http

import (
	"context"
	"errors"
	"slices"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// redirectHostsList names the OAuth redirect allowlist's cursors.
const redirectHostsList = "oauth-redirect-hosts"

// current reads the settings of the caller's account.
func (s Server) current(ctx context.Context) (kernel.Principal, settings.Settings, error) {
	p, err := principal(ctx)
	if err != nil {
		return kernel.Principal{}, settings.Settings{}, err
	}
	st, err := s.settings.Get(ctx, p)
	if err != nil {
		// A request already past authentication when its account is deleted
		// finds it gone, which answers 404 as a write would.
		return kernel.Principal{}, settings.Settings{}, settingError(err)
	}
	return p, st, nil
}

// settingError is err as the problem it answers: 422 for a value the
// service refuses, 403 for a key or a run flipping what only a person
// may, 409 for a deletion the account's state refuses, 404 for an account
// that is gone, and err itself otherwise, which the store marks when the
// database is unreachable.
func settingError(err error) error {
	switch {
	case errors.Is(err, settings.ErrInvalid):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.ValidationFailed.Error(err.Error())
	case errors.Is(err, settings.ErrNotHuman):
		return problem.Forbidden.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, settings.ErrInvalidState):
		return problem.InvalidState.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error("The account no longer exists.")
	}
	return err
}

// PersonalApiKeysEnabled answers whether the account's members may create
// personal API keys.
//
//nolint:revive // the generated interface names it
func (s Server) PersonalApiKeysEnabled(
	ctx context.Context,
	_ api.PersonalApiKeysEnabledRequestObject,
) (api.PersonalApiKeysEnabledResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.PersonalApiKeysEnabled200JSONResponse{Body: st.PersonalAPIKeys}, nil
}

// AccountSetPersonalApiKeysEnabled allows or refuses personal API keys,
// disabling the account's existing ones when asked to while refusing.
//
//nolint:revive // the generated interface names it
func (s Server) AccountSetPersonalApiKeysEnabled(
	ctx context.Context,
	req api.AccountSetPersonalApiKeysEnabledRequestObject,
) (api.AccountSetPersonalApiKeysEnabledResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.settings.SetPersonalAPIKeys(ctx, p, req.Body.Enabled,
		deref(req.Body.DisableExistingKeys))
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountSetPersonalApiKeysEnabled200JSONResponse{Body: on}, nil
}

// AccountRequestPersonalApiKeysEnabling records the caller's request that
// the account's admins enable personal API keys.
//
//nolint:revive // the generated interface names it
func (s Server) AccountRequestPersonalApiKeysEnabling(
	ctx context.Context,
	_ api.AccountRequestPersonalApiKeysEnablingRequestObject,
) (api.AccountRequestPersonalApiKeysEnablingResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.settings.RequestPersonalAPIKeys(ctx, p, from(ctx).addr); err != nil {
		return nil, settingError(err)
	}
	return api.AccountRequestPersonalApiKeysEnabling200JSONResponse{Body: true}, nil
}

// ApiKeysManageableByNonHumans answers whether API keys may manage API
// keys.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeysManageableByNonHumans(
	ctx context.Context,
	_ api.ApiKeysManageableByNonHumansRequestObject,
) (api.ApiKeysManageableByNonHumansResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.ApiKeysManageableByNonHumans200JSONResponse{Body: st.NonHumanKeyManagers}, nil
}

// AccountToggleAPIKeyManagementFromNonHumans flips whether API keys may
// manage API keys; a key or a run answers 403.
//
//nolint:revive // the generated interface names it
func (s Server) AccountToggleAPIKeyManagementFromNonHumans(
	ctx context.Context,
	_ api.AccountToggleAPIKeyManagementFromNonHumansRequestObject,
) (api.AccountToggleAPIKeyManagementFromNonHumansResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.settings.ToggleNonHumanKeyManagers(ctx, p)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountToggleAPIKeyManagementFromNonHumans200JSONResponse{Body: on}, nil
}

// ApiKeysManagedByNonAdmins answers apiKeyEnableNonAdminManagers'
// setting.
//
//nolint:revive // the generated interface names it
func (s Server) ApiKeysManagedByNonAdmins(
	ctx context.Context,
	_ api.ApiKeysManagedByNonAdminsRequestObject,
) (api.ApiKeysManagedByNonAdminsResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.ApiKeysManagedByNonAdmins200JSONResponse{Body: st.NonAdminKeyManagers}, nil
}

// SecurityEmail answers the account's security contact.
func (s Server) SecurityEmail(
	ctx context.Context,
	_ api.SecurityEmailRequestObject,
) (api.SecurityEmailResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.SecurityEmail200JSONResponse{Body: st.SecurityEmail}, nil
}

// AccountUpdateSecurityEmail sets the account's security contact.
func (s Server) AccountUpdateSecurityEmail(
	ctx context.Context,
	req api.AccountUpdateSecurityEmailRequestObject,
) (api.AccountUpdateSecurityEmailResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	email, err := s.settings.SetSecurityEmail(ctx, p, req.Body.SecurityEmail)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountUpdateSecurityEmail200JSONResponse{Body: email}, nil
}

// OauthRedirectAllowedHosts lists the account's OAuth redirect allowlist
// in the order it was set. A cursor names the last host of its page, so a
// host removed since answers 400 invalid-cursor.
func (s Server) OauthRedirectAllowedHosts(
	ctx context.Context,
	req api.OauthRedirectAllowedHostsRequestObject,
) (api.OauthRedirectAllowedHostsResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	hosts := st.RedirectHosts
	if req.Params.Cursor != nil {
		c, err := page.Decode[string](redirectHostsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		i := slices.Index(hosts, c.ID)
		if i < 0 {
			//nolint:wrapcheck // a problem, answered as it is
			return nil, problem.InvalidCursor.Error("The cursor's host is no longer allowed.")
		}
		hosts = hosts[i+1:]
	}
	limit := int(kernel.PageLimit(req.Params.Limit))
	body := api.OauthRedirectAllowedHostsPage{Items: slices.Clone(hosts[:min(len(hosts), limit)])}
	if len(hosts) > limit {
		last := hosts[limit-1]
		next, err := page.Encode(redirectHostsList, kernel.Cursor[string]{Key: last, ID: last})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding a string cannot fail
		}
		body.NextCursor = &next
	}
	return api.OauthRedirectAllowedHosts200JSONResponse{Body: body}, nil
}

// AccountSetOAuthRedirectAllowlist replaces the account's OAuth redirect
// allowlist and answers it, each host in lower case.
//
//nolint:revive // the generated interface names it
func (s Server) AccountSetOAuthRedirectAllowlist(
	ctx context.Context,
	req api.AccountSetOAuthRedirectAllowlistRequestObject,
) (api.AccountSetOAuthRedirectAllowlistResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := s.settings.SetRedirectHosts(ctx, p, req.Body.Hosts)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountSetOAuthRedirectAllowlist200JSONResponse{Body: hosts}, nil
}

// AwarenessSourceSurvey answers the account's survey answer.
func (s Server) AwarenessSourceSurvey(
	ctx context.Context,
	_ api.AwarenessSourceSurveyRequestObject,
) (api.AwarenessSourceSurveyResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.AwarenessSourceSurvey200JSONResponse{Body: api.AwarenessSourceSurvey{
		Completed: st.Survey.Completed, Response: st.Survey.Response,
	}}, nil
}

// AccountUpdateAwarenessSourceSurvey records the account's survey answer.
func (s Server) AccountUpdateAwarenessSourceSurvey(
	ctx context.Context,
	req api.AccountUpdateAwarenessSourceSurveyRequestObject,
) (api.AccountUpdateAwarenessSourceSurveyResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sv, err := s.settings.SetSurvey(ctx, p, settings.Survey{
		Completed: req.Body.Completed, Response: req.Body.Response,
	})
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountUpdateAwarenessSourceSurvey200JSONResponse{Body: api.AwarenessSourceSurvey{
		Completed: sv.Completed, Response: sv.Response,
	}}, nil
}

// MarkedForDeletion answers whether the account is marked for deletion.
func (s Server) MarkedForDeletion(
	ctx context.Context,
	_ api.MarkedForDeletionRequestObject,
) (api.MarkedForDeletionResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.MarkedForDeletion200JSONResponse{Body: st.Mark != nil}, nil
}

// AccountToggleDeletionMark marks the account for deletion, or clears the
// mark, and answers whether it is now marked.
func (s Server) AccountToggleDeletionMark(
	ctx context.Context,
	req api.AccountToggleDeletionMarkRequestObject,
) (api.AccountToggleDeletionMarkResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var reason, description *string
	if req.Body != nil {
		reason, description = req.Body.Reason, req.Body.Description
	}
	marked, err := s.settings.ToggleDeletionMark(ctx, p, reason, description)
	if err != nil {
		return nil, settingError(err)
	}
	return api.AccountToggleDeletionMark200JSONResponse{Body: marked}, nil
}

// AccountCanBeDeleted answers whether accountConfirmDelete would delete
// the account now.
func (s Server) AccountCanBeDeleted(
	ctx context.Context,
	_ api.AccountCanBeDeletedRequestObject,
) (api.AccountCanBeDeletedResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	return api.AccountCanBeDeleted200JSONResponse{Body: st.Deletable()}, nil
}

// AccountCanBeDeletedAt answers the time of the account's deletion mark in
// Unix seconds, from which it may be deleted; 0 while it is not marked.
func (s Server) AccountCanBeDeletedAt(
	ctx context.Context,
	_ api.AccountCanBeDeletedAtRequestObject,
) (api.AccountCanBeDeletedAtResponseObject, error) {
	_, st, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	var at int32
	if st.Mark != nil {
		at = unix(st.Mark.At)
	}
	return api.AccountCanBeDeletedAt200JSONResponse{Body: at}, nil
}

// AccountConfirmDelete deletes the caller's account, which must be marked
// and not its organization's last; the caller's own session goes with it.
func (s Server) AccountConfirmDelete(
	ctx context.Context,
	_ api.AccountConfirmDeleteRequestObject,
) (api.AccountConfirmDeleteResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.settings.Delete(ctx, p, from(ctx).addr); err != nil {
		return nil, settingError(err)
	}
	return api.AccountConfirmDelete200JSONResponse{Body: true}, nil
}
