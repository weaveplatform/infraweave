package pg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/reviewmeta"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

func TestAccReviewMetadataRetainsExactSwitchingSession(t *testing.T) {
	x, caller, _ := schemeFixture(t)
	ctx := t.Context()
	service := reviewmeta.New(reviewmeta.Deps{DB: x.db, Store: x.s})
	for _, scheme := range []accessapi.AuthorizationScheme{accessapi.LoginPolicies, accessapi.ManagedUsers} {
		if _, err := x.s.SetAuthorizationScheme(ctx, caller, scheme, onTrail(x, settings.ActionAuthorizationScheme)); err != nil {
			t.Fatal(err)
		}
		got, err := service.ReviewMetadata(ctx, caller, x.acct.ULID)
		if err != nil || !got.CanReview || len(got.Roles) != 1 || got.Roles[0].Slug != role.SpaceAdmin {
			t.Fatalf("retained caller's review grants under %s: %+v, %v", scheme, got, err)
		}
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM role_bindings WHERE account_id = $1`, x.acct); err != nil {
		t.Fatal(err)
	}
	retained, err := service.ReviewMetadata(ctx, caller, x.acct.ULID)
	if err != nil || !retained.CanReview || len(retained.Roles) != 1 {
		t.Fatalf("exact session lost its retained review grant: %+v, %v", retained, err)
	}
	other := x.keySession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(ctx, other); err != nil {
		t.Fatal(err)
	}
	unretained, err := service.ReviewMetadata(ctx, other.Principal(), x.acct.ULID)
	if err != nil || unretained.CanReview || len(unretained.Roles) != 0 {
		t.Fatalf("another session inherited the retained review grant: %+v, %v", unretained, err)
	}
}

func TestAccReviewMetadataRefusesEndedExactSession(t *testing.T) {
	x, caller, _ := schemeFixture(t)
	service := reviewmeta.New(reviewmeta.Deps{DB: x.db, Store: x.s})
	before, err := service.ReviewMetadata(t.Context(), caller, x.acct.ULID)
	if err != nil || !before.CanReview {
		t.Fatalf("live root session cannot review: %+v, %v", before, err)
	}
	if _, err := x.s.EndSession(t.Context(), caller.SessionID); err != nil {
		t.Fatal(err)
	}
	got, err := service.ReviewMetadata(t.Context(), caller, x.acct.ULID)
	if !errors.Is(err, kernel.ErrUnavailable) || got.AuthorKey != "" || got.CanReview || len(got.Roles) != 0 {
		t.Fatalf("ended exact session retained review facts/permission: %+v, %v", got, err)
	}
}

func TestAccReviewMetadataFromRealAccessOwners(t *testing.T) {
	x := seed(t, pool(t), "review-metadata-key")
	ctx := t.Context()
	var writer kernel.ULID
	if err := x.db.QueryRow(ctx, `SELECT id FROM roles WHERE account_id=$1 AND slug='space-writer'`, x.acct).Scan(&writer); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id,id,actor_kind,actor_id,role_id,space_id,origin,created_by)
  VALUES ($1,$2,'api_key',$3,$4,$1,'binding','test')`, x.acct, kernel.NewULID(), x.key, writer); err != nil {
		t.Fatal(err)
	}
	ss := x.keySession(time.Now().Add(time.Hour))
	ss.IPAddress = "192.0.2.6"
	if err := x.s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	service := reviewmeta.New(reviewmeta.Deps{DB: x.db, Store: x.s})
	got, err := service.ReviewMetadata(ctx, ss.Principal(), x.acct.ULID)
	if err != nil || !got.CanReview || got.AuthorKey != ss.Principal().Caller() || got.Session.ActorID != x.key || got.Session.SessionID != ss.ID || got.Identity != nil ||
		got.CreatorIP == nil || *got.CreatorIP != ss.IPAddress || got.IDPSubject != nil || len(got.Roles) != 1 || got.Roles[0].ID != writer || got.Roles[0].Slug != "space-writer" || got.Roles[0].Name != "Space writer" {
		t.Fatalf("real access owner metadata missing or manufactured: %+v, %v", got, err)
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM role_bindings WHERE account_id=$1 AND actor_kind='api_key' AND actor_id=$2`, x.acct, x.key); err != nil {
		t.Fatal(err)
	}
	revoked, err := service.ReviewMetadata(ctx, ss.Principal(), x.acct.ULID)
	if err != nil || revoked.CanReview || len(revoked.Roles) != 0 || !got.CanReview {
		t.Fatalf("review grant revocation was not fresh and immutable: current%+v prior%+v err%v", revoked, got, err)
	}
}

func TestAccReviewMetadataUsesOneAdmittedIdentityAcrossSessions(t *testing.T) {
	x := seed(t, pool(t), "review-metadata-person")
	ctx := t.Context()
	if _, err := x.db.Exec(ctx, `INSERT INTO account_users (organization_id,account_id,member_id) VALUES ($1,$2,$3)`, x.org, x.acct, x.member); err != nil {
		t.Fatal(err)
	}
	provider := kernel.NewULID()
	if _, err := x.db.Exec(
		ctx,
		`INSERT INTO member_identities (organization_id,member_id,identity_provider_id,subject) VALUES ($1,$2,$3,'subject-a')`,
		x.org,
		x.member,
		provider,
	); err != nil {
		t.Fatal(err)
	}
	roleID := kernel.NewULID()
	if _, err := x.db.Exec(ctx, `INSERT INTO roles (account_id,id,slug,name) VALUES ($1,$2,'reviewer','Original reviewer')`, x.acct, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id,id,actor_kind,actor_id,role_id,space_id,origin,created_by)
  VALUES ($1,$2,'user',$3,$4,$1,'binding','test')`, x.acct, kernel.NewULID(), x.member, roleID); err != nil {
		t.Fatal(err)
	}
	web := x.webSession(time.Now().Add(time.Hour))
	web.Name = "Alice"
	if err := x.s.CreateSession(ctx, web); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `UPDATE sessions SET idp_subject='recorded-session-subject' WHERE id=$1`, web.ID); err != nil {
		t.Fatal(err)
	}
	personal := x.keySession(time.Now().Add(time.Hour))
	personal.MemberID = x.member
	personal.Login = "alice"
	personal.Name = "Alice's key"
	if err := x.s.CreateSession(ctx, personal); err != nil {
		t.Fatal(err)
	}
	service := reviewmeta.New(reviewmeta.Deps{DB: x.db, Store: x.s})
	first, err := service.ReviewMetadata(ctx, web.Principal(), x.acct.ULID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReviewMetadata(ctx, personal.Principal(), x.acct.ULID)
	if err != nil {
		t.Fatal(err)
	}
	if first.AuthorKey != "member:"+x.member.String() || first.AuthorKey != second.AuthorKey || first.Session.SessionID == second.Session.SessionID ||
		first.Identity == nil || second.Identity == nil || first.Identity.Member != x.member || second.Identity.Member != x.member ||
		len(first.Identity.Federations) != 1 || first.Identity.Federations[0].Provider != provider || first.Identity.Federations[0].Subject != "subject-a" ||
		first.IDPSubject == nil || *first.IDPSubject != "recorded-session-subject" || second.IDPSubject != nil {
		t.Fatalf("session/key identity snapshot changed: first%+v second%+v", first, second)
	}
	if len(first.Roles) != 1 || first.Roles[0].Name != "Original reviewer" {
		t.Fatalf("review role snapshot=%+v", first.Roles)
	}
	if _, err := x.db.Exec(ctx, `UPDATE roles SET name='Renamed reviewer' WHERE account_id=$1 AND id=$2`, x.acct, roleID); err != nil {
		t.Fatal(err)
	}
	later, err := service.ReviewMetadata(ctx, web.Principal(), x.acct.ULID)
	if err != nil || len(later.Roles) != 1 || later.Roles[0].Name != "Renamed reviewer" || first.Roles[0].Name != "Original reviewer" {
		t.Fatalf("recorded/current role metadata conflated: first%+v later%+v error%v", first.Roles, later.Roles, err)
	}
	for name, mutate := range map[string]func(*kernel.Principal){
		"another account":      func(p *kernel.Principal) { p.AccountID = kernel.NewAccountID() },
		"another organization": func(p *kernel.Principal) { p.OrganizationID = kernel.NewOrganizationID() },
		"another actor":        func(p *kernel.Principal) { p.ActorID = kernel.NewULID() },
		"another member":       func(p *kernel.Principal) { p.MemberID = kernel.NewMemberID() },
		"no session":           func(p *kernel.Principal) { p.SessionID = kernel.ULID{} },
		"unknown session":      func(p *kernel.Principal) { p.SessionID = kernel.NewULID() },
		"unsupported actor":    func(p *kernel.Principal) { p.Kind = kernel.PrincipalRun },
		"no account":           func(p *kernel.Principal) { p.AccountID = kernel.AccountID{} },
	} {
		t.Run(name, func(t *testing.T) {
			p := web.Principal()
			mutate(&p)
			got, err := service.ReviewMetadata(t.Context(), p, x.acct.ULID)
			if !errors.Is(err, kernel.ErrUnavailable) || got.AuthorKey != "" || got.Identity != nil || len(got.Roles) != 0 {
				t.Fatalf("mismatched owner snapshot escaped: %+v %v", got, err)
			}
		})
	}
	if got, err := service.ReviewMetadata(ctx, web.Principal(), kernel.NewULID()); !errors.Is(err, kernel.ErrUnavailable) || got.AuthorKey != "" {
		t.Fatalf("foreign space metadata escaped: %+v %v", got, err)
	}
	if _, err := x.db.Exec(ctx, `DELETE FROM account_users WHERE account_id=$1 AND member_id=$2`, x.acct, x.member); err != nil {
		t.Fatal(err)
	}
	if got, err := service.ReviewMetadata(ctx, personal.Principal(), x.acct.ULID); !errors.Is(err, kernel.ErrUnavailable) || got.Identity != nil {
		t.Fatalf("unadmitted key owner identity escaped: %+v %v", got, err)
	}
}

type disconnectedReviewMetadata struct {
	reviewmeta.Store
	db   *pgxpool.Pool
	fail string
}

func (s disconnectedReviewMetadata) disconnect(ctx context.Context, step string) error {
	if s.fail != step {
		return nil
	}
	if !tx.In(ctx) {
		return fmt.Errorf("fault injection requires the metadata transaction")
	}
	var pid int32
	if err := tx.Q(ctx, s.db).QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		return err
	}
	var ended bool
	if err := s.db.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&ended); err != nil {
		return err
	}
	if !ended {
		return fmt.Errorf("owned metadata backend did not terminate")
	}
	return nil
}

func (s disconnectedReviewMetadata) AuthzData(ctx context.Context, a kernel.AccountID) (authz.Data, error) {
	if err := s.disconnect(ctx, "snapshot"); err != nil {
		return authz.Data{}, err
	}
	return s.Store.AuthzData(ctx, a)
}

func (s disconnectedReviewMetadata) Role(ctx context.Context, a kernel.AccountID, slug string) (role.Role, error) {
	if err := s.disconnect(ctx, "role"); err != nil {
		return role.Role{}, err
	}
	return s.Store.Role(ctx, a, slug)
}

func (s disconnectedReviewMetadata) ReviewSubject(ctx context.Context, id kernel.ULID) (*string, error) {
	if err := s.disconnect(ctx, "subject"); err != nil {
		return nil, err
	}
	return s.Store.ReviewSubject(ctx, id)
}

func (s disconnectedReviewMetadata) ReviewIdentity(ctx context.Context, p kernel.Principal) (accessapi.ReviewIdentity, error) {
	if err := s.disconnect(ctx, "identity"); err != nil {
		return accessapi.ReviewIdentity{}, err
	}
	return s.Store.ReviewIdentity(ctx, p)
}

func TestAccReviewMetadataDoesNotReturnPartialFactsAfterPostgresFailure(t *testing.T) {
	x := seed(t, pool(t), "review-metadata-fault")
	ctx := t.Context()
	if _, err := x.db.Exec(ctx, `INSERT INTO account_users (organization_id,account_id,member_id) VALUES ($1,$2,$3)`, x.org, x.acct, x.member); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO role_bindings (account_id,id,actor_kind,actor_id,role_id,space_id,origin,created_by)
  SELECT $1,$2,'user',$3,r.id,$1,'binding','test' FROM roles r WHERE r.account_id=$1 AND r.slug='space-writer'`, x.acct, kernel.NewULID(), x.member); err != nil {
		t.Fatal(err)
	}
	ss := x.webSession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"identity", "subject", "snapshot", "role"} {
		t.Run(step, func(t *testing.T) {
			service := reviewmeta.New(reviewmeta.Deps{DB: x.db, Store: disconnectedReviewMetadata{Store: x.s, db: x.db, fail: step}})
			got, err := service.ReviewMetadata(t.Context(), ss.Principal(), x.acct.ULID)
			if !errors.Is(err, kernel.ErrUnavailable) || got.AuthorKey != "" || got.Identity != nil || len(got.Roles) != 0 || !got.Session.ActorID.IsZero() {
				t.Fatalf("database failure returned partial metadata: %+v %v", got, err)
			}
		})
	}
}
