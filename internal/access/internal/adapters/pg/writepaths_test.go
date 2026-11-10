package pg_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authz"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/binding"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/group"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/role"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/space"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/user"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
)

// authzWrite matches a statement that writes a table an authorization
// snapshot reads, or an admission's account_users row.
var authzWrite = regexp.MustCompile(`(?is)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+` +
	`(?:spaces|space_closure|roles|role_actions|role_bindings|managed_user_groups|account_users)\b`)

// writePath is one of the store's methods that writes what authzWrite
// matches, and a call of it that reports the accounts whose
// authz:<account> it must send: none for a write of a column no snapshot
// and no admission reads.
type writePath struct {
	method string
	write  func(ctx context.Context) ([]kernel.AccountID, error)
}

// auditWorld is the store under audit and what its write paths share.
type auditWorld struct {
	x       seeded
	tree    adminTree
	created space.Space
	ops     group.Group
	invited user.User
	bound   binding.Binding
	second  account.Account
	marked  account.Account
	org     organization.Organization
	first   account.Account
}

// writePaths are every write path of the store, in an order in which each
// call finds what the earlier ones made.
func writePaths(w *auditWorld) []writePath {
	one := func(err error) ([]kernel.AccountID, error) { return []kernel.AccountID{w.x.acct}, err }
	return []writePath{
		{"ClaimAccount", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.ClaimAccount(ctx, w.x.org, w.x.acct, w.x.member, spacesAt,
				byAdmin(w.x, orgadmin.ActionAccountClaim))
			return one(err)
		}},
		{"CreateSpace", func(ctx context.Context) (_ []kernel.AccountID, err error) {
			w.created, err = w.tree.Create(ctx, w.x.acct, auditSlug, space.Space{Name: "Prod"})
			return one(err)
		}},
		{"UpdateSpace", func(ctx context.Context) ([]kernel.AccountID, error) {
			return one(w.tree.Move(ctx, w.x.acct, w.created.Slug, space.LegacySlug))
		}},
		{"DeleteSpace", func(ctx context.Context) ([]kernel.AccountID, error) {
			return one(w.tree.Delete(ctx, w.x.acct, w.created.Slug))
		}},
		{"CreateRole", func(ctx context.Context) ([]kernel.AccountID, error) {
			return one(w.x.s.CreateRole(ctx, role.Role{
				AccountID: w.x.acct, ID: kernel.NewULID(), Slug: "ops", Name: "Ops", Version: 1,
				Actions: []accessapi.Action{"SPACE_READ"},
			}))
		}},
		{"UpdateRole", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.UpdateRole(ctx, w.x.acct, "ops", func(r role.Role) (role.Role, error) {
				r.Actions = []accessapi.Action{"SPACE_READ", "STACK_MANAGE"}
				return r, nil
			})
			return one(err)
		}},
		{"CreateBindings", func(ctx context.Context) ([]kernel.AccountID, error) {
			ops, err := w.x.s.RoleID(ctx, w.x.acct, "ops")
			if err != nil {
				return nil, err
			}
			w.bound.RoleID, w.bound.SpaceID = ops, w.x.acct.ULID
			_, err = w.x.s.CreateBindings(ctx, w.x.acct, []binding.Binding{w.bound})
			return one(err)
		}},
		{"UpdateBinding", func(ctx context.Context) ([]kernel.AccountID, error) {
			reader, err := w.x.s.RoleID(ctx, w.x.acct, role.SpaceReader)
			if err != nil {
				return nil, err
			}
			_, err = w.x.s.UpdateBinding(ctx, w.x.acct, binding.ActorUser, w.bound.ID,
				func(l binding.Listed) (binding.Binding, error) {
					b := l.Binding
					b.RoleID = reader
					return b, nil
				})
			return one(err)
		}},
		{"DeleteBinding", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.DeleteBinding(ctx, w.x.acct, binding.ActorUser, w.bound.ID,
				func(binding.Listed) error { return nil })
			return one(err)
		}},
		{"ReplaceAccessRules", func(ctx context.Context) ([]kernel.AccountID, error) {
			rule := w.bound
			rule.ID, rule.Origin = kernel.NewULID(), binding.OriginAccessRule
			return one(w.x.s.ReplaceAccessRules(ctx, w.x.acct, binding.ActorUser,
				w.x.member.ULID, []binding.Binding{rule}))
		}},
		{"DeleteActorBindings", func(ctx context.Context) ([]kernel.AccountID, error) {
			return one(w.x.s.DeleteActorBindings(ctx, w.x.acct, binding.ActorUser, w.x.member.ULID))
		}},
		{"DeleteRole", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.DeleteRole(ctx, w.x.acct, "ops", func(role.Role) error { return nil })
			return one(err)
		}},
		{"CreateGroup", func(ctx context.Context) (_ []kernel.AccountID, err error) {
			w.ops, err = w.x.s.CreateGroup(ctx, group.Group{
				AccountID: w.x.acct, ID: kernel.NewULID(), Name: "ops", CreatedAt: spacesAt,
			}, onTrail(w.x, group.ActionCreate))
			return one(err)
		}},
		{"SetDescription", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.SetDescription(ctx, w.x.acct, w.ops.ID, kernel.Expect{}, "on call",
				onTrail(w.x, group.ActionUpdate))
			return nil, err
		}},
		{"DeleteGroup", func(ctx context.Context) ([]kernel.AccountID, error) {
			return one(w.x.s.DeleteGroup(ctx, w.x.acct, w.ops.ID, kernel.Expect{},
				onTrail(w.x, group.ActionDelete)))
		}},
		{"Invite", func(ctx context.Context) (_ []kernel.AccountID, err error) {
			w.invited, err = w.x.s.Invite(ctx, w.x.org, w.x.acct,
				user.Invite{Login: "bob", Email: "bob@example.com"},
				invitation("bob@example.com", spacesAt), onTrail(w.x, user.ActionInvite))
			return one(err)
		}},
		{"SetSlackMemberID", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.SetSlackMemberID(ctx, w.x.acct, w.invited.Member.ID, kernel.Expect{},
				"U123", onTrail(w.x, user.ActionUpdate))
			return nil, err
		}},
		{"Remove", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.Remove(ctx, w.x.acct, w.invited.Member.ID, kernel.Expect{},
				onTrail(w.x, user.ActionDelete))
			return one(err)
		}},
		{"DeleteAPIKey", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, _, err := w.x.s.DeleteAPIKey(ctx, w.x.acct, w.x.key, kernel.Expect{})
			return one(err)
		}},
		{"CreateAccount", func(ctx context.Context) ([]kernel.AccountID, error) {
			for _, a := range []account.Account{w.second, w.marked} {
				if err := w.x.s.CreateAccount(ctx, a, w.x.member,
					byAdmin(w.x, orgadmin.ActionAccountCreate)); err != nil {
					return nil, err
				}
			}
			return []kernel.AccountID{w.second.ID, w.marked.ID}, nil
		}},
		{"RemoveAccount", func(ctx context.Context) ([]kernel.AccountID, error) {
			_, err := w.x.s.RemoveAccount(ctx, w.x.org, w.second.ID,
				byAdmin(w.x, orgadmin.ActionAccountDelete))
			return []kernel.AccountID{w.second.ID}, err
		}},
		{"DeleteAccount", func(ctx context.Context) ([]kernel.AccountID, error) {
			if _, err := w.x.s.ToggleDeletionMark(ctx, w.marked.ID,
				settings.Mark{At: spacesAt}); err != nil {
				return nil, err
			}
			_, err := w.x.s.DeleteAccount(ctx, w.marked.ID, deleted(w.x, w.marked.ID))
			return []kernel.AccountID{w.marked.ID}, err
		}},
		{"CreateOrganization", func(ctx context.Context) ([]kernel.AccountID, error) {
			return []kernel.AccountID{w.first.ID},
				w.x.s.CreateOrganization(ctx, w.org, w.first, nil, created(w.org))
		}},
		{"DeleteOrganization", func(ctx context.Context) ([]kernel.AccountID, error) {
			e := byAdmin(w.x, "organization.delete")
			e.OrganizationID = w.org.ID
			_, err := w.x.s.DeleteOrganization(ctx, w.org.ID, e)
			return []kernel.AccountID{w.first.ID}, err
		}},
	}
}

// TestUnitAuthzWritePaths checks that writePaths names every exported
// method of the store that reaches a statement authzWrite matches, through
// any chain of the package's functions and variables, and nothing else: a
// new write path fails here until TestAccAuthzWritePaths exercises it.
func TestUnitAuthzWritePaths(t *testing.T) {
	got := storeWritePaths(t)
	paths := writePaths(&auditWorld{})
	want := make([]string, 0, len(paths))
	for _, p := range paths {
		want = append(want, p.method)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the store's write paths are %v, the audit exercises %v", got, want)
	}
}

// storeWritePaths parses the package's non-test files and returns, sorted,
// the exported methods of Store from which a statement authzWrite matches
// is reachable.
func storeWritePaths(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	g := newCallGraph(files)
	var out []string
	for name := range g.uses {
		method, ok := strings.CutPrefix(name, "Store.")
		if ok && ast.IsExported(method) && g.writes(name, map[string]bool{}) {
			out = append(out, method)
		}
	}
	slices.Sort(out)
	return out
}

// callGraph is, for each of the package's functions, Store's methods and
// package-level variables, the others it names and whether its own
// literals write.
type callGraph struct {
	uses   map[string]map[string]bool
	writer map[string]bool
}

func newCallGraph(files []*ast.File) callGraph {
	g := callGraph{uses: map[string]map[string]bool{}, writer: map[string]bool{}}
	top := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					top[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if v, ok := s.(*ast.ValueSpec); ok {
						for _, n := range v.Names {
							top[n.Name] = true
						}
					}
				}
			}
		}
	}
	visit := func(name, recv string, n ast.Node) {
		uses := map[string]bool{}
		g.uses[name] = uses
		ast.Inspect(n, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING && authzWrite.MatchString(x.Value) {
					g.writer[name] = true
				}
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && recv != "" && id.Name == recv {
					uses["Store."+x.Sel.Name] = true
				}
			case *ast.Ident:
				if top[x.Name] && x.Name != name {
					uses[x.Name] = true
				}
			}
			return true
		})
	}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					visit(d.Name.Name, "", d)
					continue
				}
				if name, recv, ok := storeMethod(d); ok {
					visit(name, recv, d)
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					if v, ok := s.(*ast.ValueSpec); ok {
						for _, n := range v.Names {
							visit(n.Name, "", v)
						}
					}
				}
			}
		}
	}
	return g
}

// storeMethod is d's name as Store.Name and its receiver's name, when d is
// a method of Store.
func storeMethod(d *ast.FuncDecl) (string, string, bool) {
	r := d.Recv.List[0]
	typ := r.Type
	if s, ok := typ.(*ast.StarExpr); ok {
		typ = s.X
	}
	if id, ok := typ.(*ast.Ident); !ok || id.Name != "Store" {
		return "", "", false
	}
	recv := ""
	if len(r.Names) > 0 {
		recv = r.Names[0].Name
	}
	return "Store." + d.Name.Name, recv, true
}

// writes reports whether a writing literal is reachable from name.
func (g callGraph) writes(name string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	seen[name] = true
	if g.writer[name] {
		return true
	}
	for use := range g.uses[name] {
		if g.writes(use, seen) {
			return true
		}
	}
	return false
}

// TestAccAuthzWritePaths runs every write path and checks that each one
// that changes what a snapshot reads, or an admission, moves its
// accounts' authz_version, sends their authz:<account> messages on commit
// and forgets this replica's snapshots once it commits; that a write of a
// column neither reads does none of it; and that a write rolled back with
// its caller's transaction does none of it either.
func TestAccAuthzWritePaths(t *testing.T) {
	ctx := t.Context()
	x := seed(t, pool(t), auditSlug)
	var forgot []kernel.AccountID
	x.s.WithSnapshots(func(a kernel.AccountID) { forgot = append(forgot, a) })
	c := listen(t, x.db)
	w := &auditWorld{x: x, tree: treeOf(x.s)}
	w.bound = binding.Binding{
		AccountID: x.acct,
		ID:        kernel.NewULID(),
		ActorKind: binding.ActorUser,
		ActorID:   x.member.ULID,
		Origin:    binding.OriginBinding,
		CreatedAt: spacesAt,
		CreatedBy: "audit",
	}
	w.second, w.marked = newAccount(t, x, "endframe-two"), newAccount(t, x, "endframe-three")
	w.org, w.first = tenant(t, "endframe-other", "endframe-other")

	for _, p := range writePaths(w) {
		before := versions(t, x)
		forgot = nil
		accts, err := p.write(ctx)
		if err != nil {
			t.Fatalf("%s: %v", p.method, err)
		}
		want := make([]string, 0, len(accts))
		for _, a := range accts {
			want = append(want, authz.Key(a))
		}
		slices.Sort(want)
		if got := authzMessages(t, c); !slices.Equal(got, want) {
			t.Errorf("%s sent %v, want %v", p.method, got, want)
		}
		if !sameAccounts(forgot, accts) {
			t.Errorf("%s forgot %v, want %v", p.method, forgot, accts)
		}
		after := versions(t, x)
		for _, a := range accts {
			if v, held := after[a]; held && v <= before[a] {
				t.Errorf("%s left account %s at authz_version %d", p.method, a, v)
			}
		}
		if len(accts) == 0 && after[x.acct] != before[x.acct] {
			t.Errorf("%s moved authz_version to %d", p.method, after[x.acct])
		}
	}

	// A write in its caller's transaction that then rolls back sends and
	// forgets nothing.
	forgot = nil
	errRollback := errors.New("rolled back")
	err := tx.Do(ctx, x.db, tx.Options{}, func(ctx context.Context) error {
		if _, err := x.s.CreateGroup(ctx, group.Group{
			AccountID: x.acct, ID: kernel.NewULID(), Name: "gone", CreatedAt: spacesAt,
		}, onTrail(x, group.ActionCreate)); err != nil {
			return err
		}
		if len(forgot) != 0 {
			t.Errorf("forgot %v before the commit", forgot)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("the rolled back write = %v", err)
	}
	if got := authzMessages(t, c); len(got) != 0 || len(forgot) != 0 {
		t.Errorf("a rolled back write sent %v and forgot %v", got, forgot)
	}
}

// auditSlug is the audited account's slug, and its top space's.
const auditSlug = "endframe"

// newAccount is a new account of x's organization, claimed, named slug.
func newAccount(t *testing.T, x seeded, slug string) account.Account {
	t.Helper()
	a, err := account.New(x.org, "", slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	a.CreatedAt, a.ClaimedAt = spacesAt, spacesAt
	return a
}

// versions are the authz_version of every account.
func versions(t *testing.T, x seeded) map[kernel.AccountID]int64 {
	t.Helper()
	rows, err := x.db.Query(t.Context(), `SELECT id, authz_version FROM accounts`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[kernel.AccountID]int64{}
	for rows.Next() {
		var (
			id kernel.AccountID
			v  int64
		)
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// authzMessages are the authz:<account> payloads c received, sorted.
func authzMessages(t *testing.T, c *pgx.Conn) []string {
	t.Helper()
	var out []string
	for _, m := range messages(t, c) {
		if _, ok := authz.ParseKey(m); ok {
			out = append(out, m)
		}
	}
	return out
}

// sameAccounts reports whether got and want hold the same accounts.
func sameAccounts(got, want []kernel.AccountID) bool {
	a, b := slices.Clone(got), slices.Clone(want)
	cmp := func(x, y kernel.AccountID) int { return strings.Compare(x.String(), y.String()) }
	slices.SortFunc(a, cmp)
	slices.SortFunc(b, cmp)
	return slices.Equal(a, b)
}
