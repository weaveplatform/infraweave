package pg

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/sql"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

var _ port.AccountEntry = (*Store)(nil)

// Enter commits an account session only while the verified credential remains
// usable. The account fence also orders policy/scheme changes and revocations
// before the session and its account-qualified grants become visible.
func (s *Store) Enter(ctx context.Context, candidate session.Session, verified *apikey.Key) (session.Session, error) {
	var entered session.Session
	var denied *session.LoginDenied
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		entered = candidate
		denied = nil
		q := tx.Q(ctx, s.db)
		scheme, err := lockAccount(ctx, q, candidate.AccountID)
		if err != nil {
			return err
		}
		if verified != nil {
			if err := s.entryCredential(ctx, q, &entered, *verified, scheme); err != nil {
				return err
			}
		}
		var grants []binding.Binding
		if scheme == accessapi.LoginPolicies {
			var err error
			grants, denied, err = s.loginEntry(ctx, &entered, verified)
			if err != nil || denied != nil {
				// A policy refusal commits its evaluation events; it never
				// reaches session insertion or revokes another live session.
				return err
			}
		}
		if err := s.CreateSession(ctx, entered); err != nil {
			return err
		}
		for _, grant := range grants {
			if _, err := q.Exec(ctx, `INSERT INTO session_grants
				(account_id, id, session_id, role_id, space_id, origin)
				VALUES ($1, $2, $3, $4, $5, $6)`, entered.AccountID, kernel.NewULID(), entered.ID,
				grant.RoleID, grant.SpaceID, grant.Origin); err != nil {
				return fmt.Errorf("access: store LOGIN grant: %w", sql.Unavailable(err))
			}
		}
		if len(grants) > 0 {
			return s.bumpAuthz(ctx, q, entered.AccountID)
		}
		return nil
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("enter account %s: %w", candidate.AccountID, sql.Unavailable(err))
	}
	if denied != nil {
		return session.Session{}, denied
	}
	return entered, nil
}

func (s *Store) entryCredential(ctx context.Context, q tx.Querier, ss *session.Session, verified apikey.Key, scheme accessapi.AuthorizationScheme) error {
	if ss.AccountID != verified.AccountID || ss.ActorKind != kernel.PrincipalAPIKey || ss.ActorID != verified.ID {
		return port.ErrCredentialChanged
	}
	// Member lifecycle changes take the member before its keys. Holding that
	// same order prevents a deactivation from racing a fresh personal session.
	if verified.Personal() {
		var active bool
		err := q.QueryRow(ctx, `SELECT status = 'active' FROM members
			WHERE organization_id = $1 AND id = $2 FOR SHARE`, verified.OrganizationID, verified.Owner).Scan(&active)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
			return port.ErrCredentialChanged
		}
		if err != nil {
			return fmt.Errorf("access: fence key owner: %w", sql.Unavailable(err))
		}
		if scheme == accessapi.ManagedUsers {
			var owner apikey.Owner
			err := q.QueryRow(ctx, `SELECT m.login, m.name, u.latest_groups
				FROM account_users u JOIN members m ON m.id = u.member_id
				WHERE u.account_id = $1 AND u.member_id = $2 AND m.status = 'active'
				FOR SHARE OF u`, verified.AccountID, verified.Owner).Scan(&owner.Login, &owner.Name, &owner.Teams)
			if errors.Is(err, pgx.ErrNoRows) {
				return port.ErrCredentialChanged
			}
			if err != nil {
				return fmt.Errorf("access: fence key admission: %w", sql.Unavailable(err))
			}
			ss.Login, ss.Name, ss.Teams = owner.Login, owner.Name, owner.Teams
		} else {
			ss.MemberID = kernel.MemberID{}
		}
	}
	current, err := scanKey(q.QueryRow(ctx, `SELECT `+keyColumns+`
		FROM api_keys k JOIN accounts a ON a.id = k.account_id
		WHERE k.account_id = $1 AND k.id = $2 FOR SHARE OF k`, verified.AccountID, verified.ID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (current.Version != verified.Version || !current.Usable(ss.CreatedAt)) {
		return port.ErrCredentialChanged
	}
	if err != nil {
		return read(err, "API key", verified.ID)
	}
	ss.Admin = current.Admin
	return nil
}

func (s *Store) loginEntry(ctx context.Context, ss *session.Session, key *apikey.Key) ([]binding.Binding, *session.LoginDenied, error) {
	refuse := func(message string) ([]binding.Binding, *session.LoginDenied, error) {
		if message == "" {
			message = "Account entry was denied by LOGIN policies."
		}
		return nil, &session.LoginDenied{Message: message}, nil
	}
	root := key != nil && ss.Admin
	if s.login == nil {
		if root {
			return nil, nil, nil
		}
		return refuse("Account entry evaluation is unavailable.")
	}
	spaces, roles, err := s.loginMetadata(ctx, ss.AccountID)
	if err != nil {
		return nil, nil, err
	}
	facts := accessapi.LoginSession{
		Member: ss.OriginalMember, Login: ss.Login, Name: ss.Name,
		TeamsKnown: ss.OriginalTeamsKnown, Teams: slices.Clone(ss.OriginalTeams), IDPSubject: ss.IDPSubject,
	}
	if key != nil {
		facts.Machine, facts.Admin = key.Machine, &ss.Admin
	}
	request := accessapi.LoginRequest{Account: ss.AccountID, At: ss.CreatedAt, Session: facts, Spaces: spaces, Roles: roles}
	if ss.IPAddress != "" {
		request.RemoteIP = &ss.IPAddress
	}
	decision, err := s.login.EvaluateLogin(ctx, request)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // the evaluator names the operation
	}
	if root {
		return nil, nil, nil
	}
	if !decision.HasPolicies {
		if ss.OriginalMember == nil || !*ss.OriginalMember {
			return refuse("")
		}
		ss.Admin, ss.Teams = false, slices.Clone(ss.OriginalTeams)
		return nil, nil, nil
	}
	if !decision.Allow {
		return refuse(decision.Message)
	}
	grants, valid := loginGrants(ss.AccountID, decision, spaces, roles)
	if !valid {
		return refuse("LOGIN policies selected an unavailable account role or space.")
	}
	ss.Admin, ss.Teams = decision.Admin, slices.Clone(ss.OriginalTeams)
	if len(decision.Teams) > 0 {
		ss.Teams = slices.Clone(decision.Teams)
	}
	return grants, nil, nil
}

func (s *Store) loginMetadata(ctx context.Context, account kernel.AccountID) ([]accessapi.PolicySpace, []accessapi.PolicyRole, error) {
	q := tx.Q(ctx, s.db)
	spaces, err := collect(ctx, q, `SELECT id, slug, name, labels FROM spaces WHERE account_id = $1 ORDER BY id`, account,
		func(row pgx.CollectableRow) (accessapi.PolicySpace, error) {
			var space accessapi.PolicySpace
			return space, row.Scan(&space.ID, &space.Slug, &space.Name, &space.Labels)
		})
	if err != nil {
		return nil, nil, err
	}
	roles, err := collect(ctx, q, `SELECT id, slug, name FROM roles WHERE account_id = $1 ORDER BY id`, account,
		func(row pgx.CollectableRow) (accessapi.PolicyRole, error) {
			var role accessapi.PolicyRole
			return role, row.Scan(&role.ID, &role.Slug, &role.Name)
		})
	return spaces, roles, err
}

func loginGrants(account kernel.AccountID, decision accessapi.LoginEvaluation, spaces []accessapi.PolicySpace, roles []accessapi.PolicyRole) ([]binding.Binding, bool) {
	spaceIDs, roleIDs := map[string]kernel.ULID{}, map[string]kernel.ULID{}
	for _, space := range spaces {
		spaceIDs[space.Slug] = space.ID
	}
	for _, role := range roles {
		roleIDs[role.Slug] = role.ID
	}
	out := []binding.Binding{}
	add := func(space, role string, origin binding.Origin) bool {
		sp, hasSpace := spaceIDs[space]
		r, hasRole := roleIDs[role]
		if !hasSpace || !hasRole {
			return false
		}
		grant := binding.Binding{RoleID: r, SpaceID: sp, Origin: origin}
		if !slices.Contains(out, grant) {
			out = append(out, grant)
		}
		return true
	}
	for space, assigned := range decision.Roles {
		for _, role := range assigned {
			if !add(space, role, binding.OriginAssignedRole) {
				return nil, false
			}
		}
	}
	for role, assigned := range map[string][]string{"space-admin": decision.SpaceAdmin, "space-writer": decision.SpaceWrite, "space-reader": decision.SpaceRead} {
		for _, space := range assigned {
			if !add(space, role, binding.OriginAccessLevel) {
				return nil, false
			}
		}
	}
	if decision.Admin {
		for _, space := range spaces {
			if space.ID == account.ULID && !add(space.Slug, "space-admin", binding.OriginAccessLevel) {
				return nil, false
			}
		}
	}
	return out, true
}
