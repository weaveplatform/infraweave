package pg_test

import (
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestAccLoginRoleDeletionRemovesOnlyItsSessionGrants(t *testing.T) {
	x := seed(t, pool(t), "login-role-deletion")
	ctx := t.Context()
	custom := role.Role{
		AccountID: x.acct, ID: kernel.NewULID(), Slug: "temporary-reader", Name: "Temporary reader",
		Version: 1, Actions: []accessapi.Action{accessapi.ActionSpaceRead},
	}
	if err := x.s.CreateRole(ctx, custom); err != nil {
		t.Fatal(err)
	}
	ss := x.keySession(time.Now().Add(time.Hour))
	if err := x.s.CreateSession(ctx, ss); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `UPDATE accounts SET authorization_scheme='LOGIN_POLICIES' WHERE id=$1`, x.acct); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO session_grants (account_id,id,session_id,role_id,space_id,origin)
		VALUES ($1,$2,$3,$4,$1,'ASSIGNED_ROLE')`, x.acct, kernel.NewULID(), ss.ID, custom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.db.Exec(ctx, `INSERT INTO session_grants (account_id,id,session_id,role_id,space_id,origin)
		SELECT $1,$2,$3,r.id,sp.id,'ACCESS_LEVEL' FROM roles r JOIN spaces sp ON sp.account_id=r.account_id
		WHERE r.account_id=$1 AND r.slug='space-reader' AND sp.slug='legacy'`, x.acct, kernel.NewULID(), ss.ID); err != nil {
		t.Fatal(err)
	}
	decide := authz.NewService(x.s)
	x.s.WithSnapshots(decide.Drop)
	p := ss.Principal()
	if allowed, err := decide.Authorize(ctx, p, accessapi.ActionSpaceRead, x.acct.ULID); err != nil || !allowed {
		t.Fatalf("fixture's actual LOGIN role did not authorize: %v, %v", allowed, err)
	}
	if _, err := x.s.DeleteRole(ctx, x.acct, custom.Slug, func(role.Role) error { return nil }); err != nil {
		t.Fatalf("deleting a role remained blocked by its ephemeral LOGIN grants: %v", err)
	}
	if allowed, err := decide.Authorize(ctx, p, accessapi.ActionSpaceRead, x.acct.ULID); err != nil || allowed {
		t.Fatalf("deleted role remained authorized by a cached session grant: %v, %v", allowed, err)
	}
	var grants int
	if err := x.db.QueryRow(ctx, `SELECT count(*) FROM session_grants WHERE session_id=$1`, ss.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("role deletion removed unrelated session grants: %d, %v", grants, err)
	}
	live, err := x.s.Session(ctx, ss.ID)
	if err != nil || !live.Live(time.Now()) {
		t.Fatalf("role deletion ended the unrelated credential: %v", err)
	}
}
