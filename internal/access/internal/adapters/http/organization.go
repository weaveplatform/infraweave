package http

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// The Organization tag's lists' cursor names.
const (
	orgAccountsList = "organization-accounts"
	orgMembersList  = "organization-members"
	orgKeysList     = "organization-keys"
	orgUsageList    = "organization-usage"
)

// orgActor is the request's principal and client address. The
// authenticator puts a principal on every route of the tag.
func orgActor(ctx context.Context) (orgadmin.Actor, error) {
	p, err := principal(ctx)
	if err != nil {
		return orgadmin.Actor{}, err
	}
	return orgadmin.Actor{Principal: p, RemoteIP: from(ctx).addr}, nil
}

// orgAdminError is err as the problem it answers: 403 for a member without the
// organization role, 404 for what the organization does not hold, 409
// already-exists for an account slug in use and invalid-state for a state
// the write refuses, 422 for input the service or the features domain
// refuses, and err itself otherwise, which the store marks when the
// database is unreachable.
func orgAdminError(err error, what string) error {
	le, isLimit := errors.AsType[*featuresapi.AccountLimitError](err)
	switch {
	case errors.Is(err, orgadmin.ErrForbidden):
		//nolint:wrapcheck // a problem, answered as it is
		return problem.Forbidden.Error(
			"The Organization tag needs the organization role organization-admin.",
		)
	case errors.Is(err, port.ErrNotFound):
		//nolint:wrapcheck // as above
		return problem.NotFound.Error(fmt.Sprintf("No %s in this organization.", what))
	case errors.Is(err, port.ErrSlugTaken):
		return problem.AlreadyExists.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, orgadmin.ErrInvalidState):
		return problem.InvalidState.Error(err.Error()) //nolint:wrapcheck // as above
	case errors.Is(err, organization.ErrInvalid), isLimit:
		if isLimit {
			err = le
		}
		return problem.ValidationFailed.Error(err.Error()) //nolint:wrapcheck // as above
	}
	return err
}

// listPage reads one page of a list named list, whose positions parse
// parses: one more than the page asks for shows whether another follows.
func listPage[T, P any](
	list string,
	cursor *string,
	limit *int32,
	parse func(string) (P, error),
	read func(after P, n int) ([]T, error),
	id func(T) string,
) ([]T, *string, error) {
	var after P
	if cursor != nil {
		c, err := page.Decode[string](list, *cursor)
		if err != nil {
			return nil, nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		if after, err = parse(c.ID); err != nil {
			//nolint:wrapcheck // a problem, answered as it is
			return nil, nil, problem.InvalidCursor.Error(
				"The cursor's position is not of its list.",
			)
		}
	}
	n := int(kernel.PageLimit(limit))
	items, err := read(after, n+1)
	if err != nil {
		return nil, nil, err
	}
	if len(items) <= n {
		return items, nil, nil
	}
	last := id(items[n-1])
	next, err := page.Encode(list, kernel.Cursor[string]{Key: last, ID: last})
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // encoding a string cannot fail
	}
	return items[:n], &next, nil
}

func parseAccount(s string) (kernel.AccountID, error) {
	u, err := kernel.ParseULID(s)
	return kernel.AccountID{ULID: u}, err //nolint:wrapcheck // answered as an invalid cursor
}

func parseMember(s string) (kernel.MemberID, error) {
	u, err := kernel.ParseULID(s)
	return kernel.MemberID{ULID: u}, err //nolint:wrapcheck // as above
}

// Organization reads the caller's organization.
func (s Server) Organization(
	ctx context.Context,
	_ api.OrganizationRequestObject,
) (api.OrganizationResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.orgs.Organization(ctx, by)
	if err != nil {
		return nil, orgAdminError(err, "organization")
	}
	return api.Organization200JSONResponse{Body: orgViewOf(o)}, nil
}

// OrganizationUpdate renames the caller's organization.
func (s Server) OrganizationUpdate(
	ctx context.Context,
	req api.OrganizationUpdateRequestObject,
) (api.OrganizationUpdateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	o, err := s.orgs.Rename(ctx, by, req.Body.Name)
	if err != nil {
		return nil, orgAdminError(err, "organization")
	}
	return api.OrganizationUpdate200JSONResponse{Body: orgViewOf(o)}, nil
}

// OrganizationAccounts lists the organization's accounts, oldest first.
func (s Server) OrganizationAccounts(
	ctx context.Context,
	req api.OrganizationAccountsRequestObject,
) (api.OrganizationAccountsResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	accts, next, err := listPage(orgAccountsList, req.Params.Cursor, req.Params.Limit, parseAccount,
		func(after kernel.AccountID, n int) ([]account.Account, error) {
			return s.orgs.Accounts(ctx, by, after, n)
		},
		func(a account.Account) string { return a.ID.String() })
	if err != nil {
		return nil, orgAdminError(err, "account")
	}
	body := api.OrganizationAccountPage{
		Items:      make([]api.OrganizationAccount, 0, len(accts)),
		NextCursor: next,
	}
	for _, a := range accts {
		body.Items = append(body.Items, orgAccountView(a))
	}
	return api.OrganizationAccounts200JSONResponse{Body: body}, nil
}

// OrganizationAccountCreate creates an account claimed by the member the
// body names.
func (s Server) OrganizationAccountCreate(
	ctx context.Context,
	req api.OrganizationAccountCreateRequestObject,
) (api.OrganizationAccountCreateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.orgs.CreateAccount(ctx, by, req.Body.Slug, req.Body.Name, req.Body.Admin)
	if err != nil {
		return nil, accountError(err, req.Body.Slug)
	}
	return api.OrganizationAccountCreate201JSONResponse{Body: orgAccountView(a)}, nil
}

// OrganizationAccountClaim claims an unclaimed account.
func (s Server) OrganizationAccountClaim(
	ctx context.Context,
	req api.OrganizationAccountClaimRequestObject,
) (api.OrganizationAccountClaimResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.orgs.Claim(ctx, by, req.Account, req.Body.Admin)
	if err != nil {
		return nil, accountError(err, req.Account)
	}
	return api.OrganizationAccountClaim200JSONResponse{Body: orgAccountView(a)}, nil
}

// OrganizationAccountUpdate renames an account.
func (s Server) OrganizationAccountUpdate(
	ctx context.Context,
	req api.OrganizationAccountUpdateRequestObject,
) (api.OrganizationAccountUpdateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.orgs.RenameAccount(ctx, by, req.Account, req.Body.Name)
	if err != nil {
		return nil, accountError(err, req.Account)
	}
	return api.OrganizationAccountUpdate200JSONResponse{Body: orgAccountView(a)}, nil
}

// OrganizationAccountDelete deletes an account and answers it as it was.
func (s Server) OrganizationAccountDelete(
	ctx context.Context,
	req api.OrganizationAccountDeleteRequestObject,
) (api.OrganizationAccountDeleteResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.orgs.DeleteAccount(ctx, by, req.Account)
	if err != nil {
		return nil, accountError(err, req.Account)
	}
	return api.OrganizationAccountDelete200JSONResponse{Body: orgAccountView(a)}, nil
}

// OrganizationAccountLimitSet sets an account limit.
func (s Server) OrganizationAccountLimitSet(
	ctx context.Context,
	req api.OrganizationAccountLimitSetRequestObject,
) (api.OrganizationAccountLimitSetResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.orgs.SetAccountLimit(ctx, by, req.Account, req.Limit, req.Body.Value)
	if err != nil {
		return nil, accountError(err, req.Account)
	}
	return api.OrganizationAccountLimitSet200JSONResponse{Body: usageView(u)}, nil
}

// OrganizationAccountLimitClear clears an account limit.
func (s Server) OrganizationAccountLimitClear(
	ctx context.Context,
	req api.OrganizationAccountLimitClearRequestObject,
) (api.OrganizationAccountLimitClearResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	u, err := s.orgs.ClearAccountLimit(ctx, by, req.Account, req.Limit)
	if err != nil {
		return nil, accountError(err, req.Account)
	}
	return api.OrganizationAccountLimitClear200JSONResponse{Body: usageView(u)}, nil
}

// accountError is orgAdminError for the account slug.
func accountError(err error, slug string) error {
	return orgAdminError(err, fmt.Sprintf("account %q", slug))
}

// OrganizationMembers lists the organization's members, oldest first.
func (s Server) OrganizationMembers(
	ctx context.Context,
	req api.OrganizationMembersRequestObject,
) (api.OrganizationMembersResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	ms, next, err := listPage(orgMembersList, req.Params.Cursor, req.Params.Limit, parseMember,
		func(after kernel.MemberID, n int) ([]orgadmin.Member, error) {
			return s.orgs.Members(ctx, by, after, n)
		},
		func(m orgadmin.Member) string { return m.ID.String() })
	if err != nil {
		return nil, orgAdminError(err, "member")
	}
	body := api.OrganizationMemberPage{
		Items:      make([]api.OrganizationMember, 0, len(ms)),
		NextCursor: next,
	}
	for _, m := range ms {
		body.Items = append(body.Items, memberView(m))
	}
	return api.OrganizationMembers200JSONResponse{Body: body}, nil
}

// OrganizationMemberDeactivate deactivates a member.
func (s Server) OrganizationMemberDeactivate(
	ctx context.Context,
	req api.OrganizationMemberDeactivateRequestObject,
) (api.OrganizationMemberDeactivateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.orgs.Deactivate(ctx, by, req.Member)
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("member %q", req.Member))
	}
	return api.OrganizationMemberDeactivate200JSONResponse{Body: memberView(m)}, nil
}

// OrganizationAdminGrant grants a member organization-admin.
func (s Server) OrganizationAdminGrant(
	ctx context.Context,
	req api.OrganizationAdminGrantRequestObject,
) (api.OrganizationAdminGrantResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.orgs.SetAdmin(ctx, by, req.Member, true)
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("member %q", req.Member))
	}
	return api.OrganizationAdminGrant200JSONResponse{Body: memberView(m)}, nil
}

// OrganizationAdminRevoke revokes a member's organization-admin.
func (s Server) OrganizationAdminRevoke(
	ctx context.Context,
	req api.OrganizationAdminRevokeRequestObject,
) (api.OrganizationAdminRevokeResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.orgs.SetAdmin(ctx, by, req.Member, false)
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("member %q", req.Member))
	}
	return api.OrganizationAdminRevoke200JSONResponse{Body: memberView(m)}, nil
}

// OrganizationKeys lists the organization's keys, oldest first, with no
// secret.
func (s Server) OrganizationKeys(
	ctx context.Context,
	req api.OrganizationKeysRequestObject,
) (api.OrganizationKeysResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	ks, next, err := listPage(orgKeysList, req.Params.Cursor, req.Params.Limit, kernel.ParseULID,
		func(after kernel.ULID, n int) ([]apikey.OrganizationKey, error) {
			return s.orgs.Keys(ctx, by, after, n)
		},
		func(k apikey.OrganizationKey) string { return k.ID.String() })
	if err != nil {
		return nil, orgAdminError(err, "organization key")
	}
	body := api.OrganizationKeyPage{
		Items:      make([]api.OrganizationKey, 0, len(ks)),
		NextCursor: next,
	}
	for _, k := range ks {
		body.Items = append(body.Items, orgKeyView(k, ""))
	}
	return api.OrganizationKeys200JSONResponse{Body: body}, nil
}

// OrganizationKeyCreate creates an organization key and answers its
// secret, once.
func (s Server) OrganizationKeyCreate(
	ctx context.Context,
	req api.OrganizationKeyCreateRequestObject,
) (api.OrganizationKeyCreateResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	var expires time.Time
	if req.Body.ExpiresAt != nil {
		expires = time.Unix(*req.Body.ExpiresAt, 0)
	}
	k, secret, err := s.orgs.CreateKey(ctx, by, req.Body.Name, expires)
	if err != nil {
		return nil, orgAdminError(err, "organization key")
	}
	return api.OrganizationKeyCreate201JSONResponse{Body: orgKeyView(k, secret)}, nil
}

// OrganizationKeyDisable disables an organization key.
func (s Server) OrganizationKeyDisable(
	ctx context.Context,
	req api.OrganizationKeyDisableRequestObject,
) (api.OrganizationKeyDisableResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	k, err := s.orgs.DisableKey(ctx, by, req.Key)
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("organization key %q", req.Key))
	}
	return api.OrganizationKeyDisable200JSONResponse{Body: orgKeyView(k, "")}, nil
}

// OrganizationKeyDelete deletes an organization key and answers it as it
// was.
func (s Server) OrganizationKeyDelete(
	ctx context.Context,
	req api.OrganizationKeyDeleteRequestObject,
) (api.OrganizationKeyDeleteResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	k, err := s.orgs.DeleteKey(ctx, by, req.Key)
	if err != nil {
		return nil, orgAdminError(err, fmt.Sprintf("organization key %q", req.Key))
	}
	return api.OrganizationKeyDelete200JSONResponse{Body: orgKeyView(k, "")}, nil
}

// OrganizationLimits answers the pooled limits and their use.
func (s Server) OrganizationLimits(
	ctx context.Context,
	_ api.OrganizationLimitsRequestObject,
) (api.OrganizationLimitsResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	ls, err := s.orgs.Limits(ctx, by)
	if err != nil {
		return nil, orgAdminError(err, "organization")
	}
	return api.OrganizationLimits200JSONResponse{
		Body: api.OrganizationLimits{Items: limitsView(ls)},
	}, nil
}

// OrganizationUsage answers each account's use of every limit, oldest
// account first.
func (s Server) OrganizationUsage(
	ctx context.Context,
	req api.OrganizationUsageRequestObject,
) (api.OrganizationUsageResponseObject, error) {
	by, err := orgActor(ctx)
	if err != nil {
		return nil, err
	}
	us, next, err := listPage(orgUsageList, req.Params.Cursor, req.Params.Limit, parseAccount,
		func(after kernel.AccountID, n int) ([]orgadmin.AccountUsage, error) {
			return s.orgs.Usage(ctx, by, after, n)
		},
		func(u orgadmin.AccountUsage) string { return u.Account.ID.String() })
	if err != nil {
		return nil, orgAdminError(err, "account")
	}
	body := api.OrganizationUsagePage{
		Items:      make([]api.OrganizationAccountUsage, 0, len(us)),
		NextCursor: next,
	}
	for _, u := range us {
		body.Items = append(body.Items, usageView(u))
	}
	return api.OrganizationUsage200JSONResponse{Body: body}, nil
}

func orgViewOf(o organization.Organization) api.OrganizationView {
	return api.OrganizationView{
		Id: o.ID.String(), Slug: string(o.Slug), Name: o.Name, CreatedAt: o.CreatedAt.Unix(),
	}
}

func orgAccountView(a account.Account) api.OrganizationAccount {
	return api.OrganizationAccount{
		Id: a.ID.String(), Slug: string(a.Slug), Name: a.Name, Claimed: a.Claimed(),
		CreatedAt: a.CreatedAt.Unix(),
	}
}

func memberView(m orgadmin.Member) api.OrganizationMember {
	status := api.OrganizationMemberStatusDEACTIVATED
	if m.Active {
		status = api.OrganizationMemberStatusACTIVE
	}
	return api.OrganizationMember{
		Id: m.ID.String(), Login: m.Login, Name: m.Name, Email: m.Email,
		OrganizationAdmin: m.Admin, Status: status, CreatedAt: m.CreatedAt.Unix(),
	}
}

// orgKeyView is k in the tag's shape, with secret when it is not empty.
func orgKeyView(k apikey.OrganizationKey, secret string) api.OrganizationKey {
	out := api.OrganizationKey{
		Id: k.ID.String(), Name: k.Name, Disabled: !k.DisabledAt.IsZero(),
		CreatedAt: k.CreatedAt.Unix(), ExpiresAt: unixOrNil(k.ExpiresAt),
		LastUsedAt: unixOrNil(k.LastUsedAt),
	}
	if secret != "" {
		out.Secret = &secret
	}
	return out
}

func usageView(u orgadmin.AccountUsage) api.OrganizationAccountUsage {
	return api.OrganizationAccountUsage{
		Account: orgAccountView(u.Account),
		Limits:  limitsView(u.Limits),
	}
}

func limitsView(ls []orgadmin.Limit) []api.OrganizationLimit {
	out := make([]api.OrganizationLimit, 0, len(ls))
	for _, l := range ls {
		out = append(out, api.OrganizationLimit{Name: l.Name, Value: l.Value, InUse: l.InUse})
	}
	return out
}

func unixOrNil(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	u := t.Unix()
	return &u
}
