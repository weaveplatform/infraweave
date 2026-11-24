package pg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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
// before the session and its account-qualified grants become visible. A
// member session's entry records the groups its sign-in reported in the
// same transaction, so the session and the admission's latest_groups never
// disagree.
func (s *Store) Enter(ctx context.Context, candidate session.Session, verified *apikey.Key, source kernel.ULID) (session.Session, error) {
	var entered session.Session
	var denied *session.LoginDenied
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		entered = candidate
		denied = nil
		grants, refused, err := s.prepareEntry(ctx, &entered, verified, source)
		denied = refused
		if err != nil || denied != nil {
			// A policy refusal commits its evaluation events; it never
			// reaches session insertion or revokes another live session.
			return err
		}
		if verified == nil {
			if err := recordGroups(ctx, tx.Q(ctx, s.db), entered); err != nil {
				return err
			}
		}
		if err := s.CreateSession(ctx, entered); err != nil {
			return err
		}
		if !source.IsZero() {
			// The logout of the entered session hints the provider with the
			// ID token its source was signed in with.
			if err := s.carryIDToken(ctx, tx.Q(ctx, s.db), source, entered.ID); err != nil {
				return err
			}
		}
		return s.installLoginGrants(ctx, entered, grants)
	})
	if err != nil {
		return session.Session{}, fmt.Errorf("enter account %s: %w", candidate.AccountID, sql.Unavailable(err))
	}
	if denied != nil {
		return session.Session{}, denied
	}
	return entered, nil
}

func (s *Store) installLoginGrants(ctx context.Context, ss session.Session, grants []binding.Binding) error {
	q := tx.Q(ctx, s.db)
	for _, grant := range grants {
		if _, err := q.Exec(ctx, `INSERT INTO session_grants
			(account_id, id, session_id, role_id, space_id, origin)
			VALUES ($1, $2, $3, $4, $5, $6)`, ss.AccountID, kernel.NewULID(), ss.ID,
			grant.RoleID, grant.SpaceID, grant.Origin); err != nil {
			return fmt.Errorf("access: store LOGIN grant: %w", sql.Unavailable(err))
		}
	}
	if len(grants) > 0 {
		return s.bumpAuthz(ctx, q, ss.AccountID)
	}
	return nil
}

func (s *Store) prepareEntry(ctx context.Context, ss *session.Session, key *apikey.Key, source kernel.ULID) ([]binding.Binding, *session.LoginDenied, error) {
	q := tx.Q(ctx, s.db)
	scheme, err := lockAccount(ctx, q, ss.AccountID)
	if errors.Is(err, port.ErrNotFound) {
		return nil, nil, session.ErrNotAdmitted
	}
	if err != nil {
		return nil, nil, err
	}
	if key != nil {
		if err := s.entryCredential(ctx, q, ss, *key, scheme); err != nil {
			return nil, nil, err
		}
	} else {
		if err := entryMember(ctx, q, ss, scheme); err != nil {
			return nil, nil, err
		}
		if err := entrySource(ctx, q, ss, source); err != nil {
			return nil, nil, err
		}
	}
	if scheme == accessapi.LoginPolicies {
		return s.loginEntry(ctx, ss, key)
	}
	return nil, nil, nil
}

func entryMember(ctx context.Context, q tx.Querier, ss *session.Session, scheme accessapi.AuthorizationScheme) error {
	if ss.ActorKind != kernel.PrincipalUser || ss.MemberID.IsZero() || ss.ActorID != ss.MemberID.ULID {
		return session.ErrNotAdmitted
	}
	var active bool
	err := q.QueryRow(ctx, `SELECT m.status='active' FROM members m JOIN accounts a ON a.organization_id=m.organization_id
		WHERE a.id=$1 AND m.organization_id=$2 AND m.id=$3 FOR SHARE OF m`,
		ss.AccountID, ss.OrganizationID, ss.MemberID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return session.ErrNotAdmitted
	}
	if err != nil {
		return fmt.Errorf("access: fence entering member: %w", sql.Unavailable(err))
	}
	if scheme == accessapi.ManagedUsers {
		var member kernel.MemberID
		err := q.QueryRow(ctx, `SELECT member_id FROM account_users WHERE account_id=$1 AND member_id=$2 FOR SHARE`,
			ss.AccountID, ss.MemberID).Scan(&member)
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotAdmitted
		}
		if err != nil {
			return fmt.Errorf("access: fence managed admission: %w", sql.Unavailable(err))
		}
	}
	return nil
}

// recordGroups writes the groups ss's sign-in reported, its original
// teams, as the latest_groups of the member's admission to the account ss
// enters, raising the admission's version when they change. A sign-in
// that reported no groups writes none, so the member leaves every managed
// user group of the account; a session whose original teams are unknown
// writes nothing. Only the entered account is written: another keeps the
// groups of the member's latest entry into it. Under LOGIN_POLICIES a
// member may enter without an admission, and then there is no row to
// write.
//
// ref: decision 0007 § "Bindings"
func recordGroups(ctx context.Context, q tx.Querier, ss session.Session) error {
	if !ss.OriginalTeamsKnown {
		return nil
	}
	groups := ss.OriginalTeams
	if groups == nil {
		groups = []string{}
	}
	if _, err := q.Exec(ctx, `UPDATE account_users SET latest_groups = $3, version = version + 1
		WHERE account_id = $1 AND member_id = $2 AND latest_groups IS DISTINCT FROM $3::text[]`,
		ss.AccountID, ss.MemberID, groups); err != nil {
		return fmt.Errorf("access: record the groups of account entry: %w", sql.Unavailable(err))
	}
	return nil
}

func entrySource(ctx context.Context, q tx.Querier, next *session.Session, id kernel.ULID) error {
	if id.IsZero() {
		return port.ErrCredentialChanged
	}
	cur, err := scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id=$1 FOR SHARE`, id))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!cur.Live(time.Now()) ||
		cur.OrganizationID != next.OrganizationID || cur.MemberID != next.MemberID ||
		cur.ActorKind != next.ActorKind || cur.ActorID != next.ActorID || next.ExpiresAt.After(cur.ExpiresAt)) {
		return port.ErrCredentialChanged
	}
	if err != nil {
		return read(err, "entering source session", id)
	}
	next.Login, next.Name, next.Email = cur.Login, cur.Name, cur.Email
	next.OriginalMember, next.OriginalTeamsKnown = cur.OriginalMember, cur.OriginalTeamsKnown
	next.OriginalTeams = slices.Clone(cur.OriginalTeams)
	next.IdentityProviderID, next.IDPSubject, next.IDPSID = cur.IdentityProviderID, cur.IDPSubject, cur.IDPSID
	next.Admin, next.Teams = false, slices.Clone(cur.Teams)
	if cur.OriginalTeamsKnown {
		next.Teams = slices.Clone(cur.OriginalTeams)
	}
	return nil
}

// AdmittedAccounts reevaluates admission without installing a session or
// grants. Each target has its own transaction, so a refusal's evidence commits
// without retaining locks across the organization or exposing its detail.
func (s *Store) AdmittedAccounts(ctx context.Context, identity session.Session) ([]session.Admitted, error) {
	if !identity.Live(time.Now()) {
		return nil, port.ErrCredentialChanged
	}
	rows, err := tx.Q(ctx, s.db).Query(ctx, `SELECT id, slug, name FROM accounts WHERE organization_id=$1 ORDER BY slug`, identity.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("access: list organization account entries: %w", sql.Unavailable(err))
	}
	accounts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (session.Admitted, error) {
		var a session.Admitted
		err := row.Scan(&a.ID, &a.Slug, &a.Name)
		return a, err //nolint:wrapcheck // the collecting caller names the scan
	})
	if err != nil {
		return nil, fmt.Errorf("access: scan organization account entries: %w", sql.Unavailable(err))
	}
	admitted := []session.Admitted{}
	for _, account := range accounts {
		var denied *session.LoginDenied
		err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
			candidate := identity
			candidate.AccountID, candidate.CreatedAt = account.ID, time.Now()
			_, refused, err := s.prepareEntry(ctx, &candidate, nil, identity.ID)
			denied = refused
			return err
		})
		if errors.Is(err, session.ErrNotAdmitted) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("access: evaluate admission to account %s: %w", account.ID, sql.Unavailable(err))
		}
		if denied == nil {
			admitted = append(admitted, account)
		}
	}
	return admitted, nil
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
