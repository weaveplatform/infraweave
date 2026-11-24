package pg_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/weaveplatform/infraweave/internal/access/internal/adapters/pg"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// seedProvider inserts an OIDC provider of org through SQL, for an
// identity a test seeds to name.
func seedProvider(t *testing.T, db *pgxpool.Pool, org kernel.OrganizationID) kernel.ULID {
	t.Helper()
	id := kernel.NewULID()
	if _, err := db.Exec(t.Context(), `INSERT INTO identity_providers (organization_id, id, kind,
		name, settings, secret_sealed) VALUES ($1, $2, 'OIDC', 'seeded', '{}', '\x00')`,
		org, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// provider is a new provider of x's organization, created at at.
func (x seeded) provider(kind sso.Kind, name string, at time.Time) sso.Provider {
	s := sso.Settings{IdentityProviderHost: "https://login.example.com", ClientID: "web"}
	switch kind {
	case sso.SAML:
		s = sso.Settings{IDPMetadataURL: "https://idp.example/md", NameIDFormat: sso.NameIDPersistent}
	case sso.GitHubOAuth:
		s = sso.Settings{BaseURL: sso.GitHubURL, ClientID: "c", Organization: "acme"}
	}
	return sso.Provider{
		OrganizationID: x.org, ID: kernel.NewULID(), Kind: kind, Name: name, Settings: s,
		CreatedAt: at, UpdatedAt: at,
	}
}

// TestAccIdentityProviders creates providers of each kind, the first of
// each its kind's default, seals each secret in secret_sealed alone, and
// keeps one default per kind through a new default and a deletion, which
// deletes the provider's identities and revokes its sessions. Each write
// records its trail entry.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccIdentityProviders(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "pied-piper")
	other := seed(t, db, "hooli-xyz")
	s := x.s.WithSealer(keyring(t, db))
	l := listen(t, db)
	t1 := time.Now().UTC().Truncate(time.Second)

	made := make([]sso.Provider, 0, 5)
	for i, c := range []struct {
		kind sso.Kind
		name string
		def  bool
	}{
		{sso.OIDC, "okta", true},
		{sso.SAML, "adfs", true},
		{sso.OIDC, "entra", false},
		{sso.GitHubOAuth, "github", true},
		{sso.OIDC, "auth0", false},
	} {
		p := x.provider(c.kind, c.name, t1.Add(time.Duration(i)*time.Minute))
		got, err := s.CreateProvider(ctx, p, []byte("secret-"+c.name),
			byAdmin(x, sso.ActionCreate))
		if err != nil || got.Default != c.def || got.Enabled() || got.Settings.ClientID != p.Settings.ClientID ||
			!got.CreatedAt.Equal(p.CreatedAt) {
			t.Fatalf("create %s = %+v, %v", c.name, got, err)
		}
		made = append(made, got)
	}
	okta, entra, auth0 := made[0], made[2], made[4]
	if n := trail(t, x, sso.ActionCreate); n != 5 {
		t.Errorf("creations on the trail = %d", n)
	}

	// The secret is sealed, bound to the row, and opens with the keyring.
	var sealed []byte
	if err := db.QueryRow(ctx, `SELECT secret_sealed FROM identity_providers WHERE id = $1`,
		okta.ID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte("secret-okta")) {
		t.Errorf("secret_sealed = %q, %v", sealed, err)
	}
	if v, err := s.ProviderSecret(ctx, x.org, okta.ID); err != nil || string(v.Reveal()) != "secret-okta" {
		t.Errorf("ProviderSecret = %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE identity_providers SET secret_sealed = $2 WHERE id = $1`,
		entra.ID, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProviderSecret(ctx, x.org, entra.ID); err == nil {
		t.Error("another provider's sealed secret opened")
	}
	if _, err := s.ProviderSecret(ctx, other.org, okta.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's secret = %v", err)
	}

	ps, err := s.Providers(ctx, x.org)
	if err != nil || len(ps) != 5 || ps[0].ID != okta.ID || ps[4].ID != auth0.ID {
		t.Fatalf("Providers = %+v, %v", ps, err)
	}
	if ps, err := s.Providers(ctx, other.org); err != nil || len(ps) != 0 {
		t.Errorf("another organization's providers = %+v, %v", ps, err)
	}
	if _, err := s.Provider(ctx, other.org, okta.ID); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's provider = %v", err)
	}

	// A new default of a kind replaces the old one.
	got, err := s.SetDefaultProvider(ctx, x.org, auth0.ID, byAdmin(x, sso.ActionSetDefault))
	if err != nil || !got.Default {
		t.Fatalf("SetDefaultProvider = %+v, %v", got, err)
	}
	if defaults := defaultsOf(t, s, x.org); !slices.Equal(defaults, []string{"adfs", "github", "auth0"}) {
		t.Errorf("defaults = %v", defaults)
	}
	if _, err := s.SetDefaultProvider(ctx, other.org, okta.ID, byAdmin(other, sso.ActionSetDefault)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's default = %v", err)
	}

	// An update writes the name, settings and a new secret; no secret
	// keeps the one there.
	okta.Name, okta.Settings.ClaimMapping = "Okta", []sso.ClaimMappingEntry{{Name: "login", Value: "upn"}}
	if got, err := s.UpdateProvider(ctx, okta, nil, byAdmin(x, sso.ActionUpdate)); err != nil ||
		got.Name != "Okta" || len(got.Settings.ClaimMapping) != 1 {
		t.Errorf("UpdateProvider = %+v, %v", got, err)
	}
	if v, err := s.ProviderSecret(ctx, x.org, okta.ID); err != nil || string(v.Reveal()) != "secret-okta" {
		t.Errorf("the kept secret = %v", err)
	}
	if _, err := s.UpdateProvider(ctx, okta, []byte("rotated"), byAdmin(x, sso.ActionUpdate)); err != nil {
		t.Fatal(err)
	}
	if v, err := s.ProviderSecret(ctx, x.org, okta.ID); err != nil || string(v.Reveal()) != "rotated" {
		t.Errorf("the rotated secret = %v", err)
	}
	stray := okta
	stray.OrganizationID = other.org
	if _, err := s.UpdateProvider(ctx, stray, nil, byAdmin(other, sso.ActionUpdate)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("another organization's update = %v", err)
	}

	// Deleting the default deletes its identities, revokes the sessions
	// signed in through it and promotes the oldest remaining provider of
	// its kind.
	if _, err := db.Exec(ctx, `INSERT INTO member_identities (organization_id, member_id,
		identity_provider_id, subject) VALUES ($1, $2, $3, 'alice'), ($1, $2, $4, 'alice')`,
		x.org, x.member, auth0.ID, okta.ID); err != nil {
		t.Fatal(err)
	}
	through := x.webSession(time.Now().Add(time.Hour))
	through.IdentityProviderID = auth0.ID
	if err := s.CreateSession(ctx, through); err != nil {
		t.Fatal(err)
	}
	messages(t, l)
	gone, ids, err := s.DeleteProvider(ctx, x.org, auth0.ID, byAdmin(x, sso.ActionDelete))
	if err != nil || gone.ID != auth0.ID || !gone.Default || !slices.Equal(ids, []kernel.ULID{through.ID}) {
		t.Fatalf("DeleteProvider = %+v, %v, %v", gone, ids, err)
	}
	if got := messages(t, l); !slices.Equal(got, keys(through.ID)) {
		t.Errorf("deletion sent %v, want %v", got, keys(through.ID))
	}
	if ss, err := s.Session(ctx, through.ID); err != nil || ss.RevokedAt.IsZero() {
		t.Errorf("the provider's session = %+v, %v", ss, err)
	}
	var identities []string
	rows, err := db.Query(ctx, `SELECT subject || '@' || identity_provider_id FROM member_identities
		WHERE organization_id = $1`, x.org)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		identities = append(identities, s)
	}
	if want := []string{"alice@" + okta.ID.UUID()}; !slices.Equal(identities, want) {
		t.Errorf("identities = %v, want %v", identities, want)
	}
	if defaults := defaultsOf(t, s, x.org); !slices.Equal(defaults, []string{"Okta", "adfs", "github"}) {
		t.Errorf("defaults after deleting auth0 = %v", defaults)
	}
	// The last of a kind leaves its kind without a default.
	if _, _, err := s.DeleteProvider(ctx, x.org, made[1].ID, byAdmin(x, sso.ActionDelete)); err != nil {
		t.Fatal(err)
	}
	// A provider that is not the default goes alone.
	if _, _, err := s.DeleteProvider(ctx, x.org, entra.ID, byAdmin(x, sso.ActionDelete)); err != nil {
		t.Fatal(err)
	}
	if defaults := defaultsOf(t, s, x.org); !slices.Equal(defaults, []string{"Okta", "github"}) {
		t.Errorf("defaults = %v", defaults)
	}
	if _, _, err := s.DeleteProvider(ctx, x.org, auth0.ID, byAdmin(x, sso.ActionDelete)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a deleted provider = %v", err)
	}
	if n := trail(t, x, sso.ActionDelete); n != 3 {
		t.Errorf("deletions on the trail = %d, want the three that succeeded", n)
	}
	if _, _, err := s.DeleteProvider(ctx, kernel.NewOrganizationID(), okta.ID, byAdmin(x, sso.ActionDelete)); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("no organization = %v", err)
	}
}

// defaultsOf are org's default providers' names, oldest first.
func defaultsOf(t *testing.T, s *pg.Store, org kernel.OrganizationID) []string {
	t.Helper()
	ps, err := s.Providers(t.Context(), org)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		if p.Default {
			out = append(out, p.Name)
		}
	}
	return out
}

// TestAccDefaultProviderCreate creates a kind's default once: a second
// create of the kind, sequential or concurrent, is sso.ErrDefaultExists
// and writes neither a provider nor a trail entry, while another kind or
// organization is unaffected.
//
// ref: decision 0006 § "Members and identity providers"
func TestAccDefaultProviderCreate(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "endframe")
	other := seed(t, db, "aviato-inc")
	s := x.s.WithSealer(keyring(t, db))
	t1 := time.Now().UTC().Truncate(time.Second)

	results := make(chan error, 2)
	for i := range 2 {
		go func() {
			_, err := s.CreateDefaultProvider(ctx, x.provider(sso.OIDC, "okta", t1.Add(time.Duration(i)*time.Second)),
				[]byte("secret"), byAdmin(x, sso.ActionCreate))
			results <- err
		}()
	}
	var created, refused int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			created++
		case errors.Is(err, sso.ErrDefaultExists):
			refused++
		default:
			t.Errorf("a concurrent create = %v", err)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("concurrent creates: %d created, %d refused", created, refused)
	}
	if defaults := defaultsOf(t, s, x.org); !slices.Equal(defaults, []string{"okta"}) {
		t.Errorf("defaults = %v", defaults)
	}
	if _, err := s.CreateDefaultProvider(ctx, x.provider(sso.OIDC, "entra", t1), []byte("s"),
		byAdmin(x, sso.ActionCreate)); !errors.Is(err, sso.ErrDefaultExists) {
		t.Errorf("a second default = %v", err)
	}
	if ps, err := s.Providers(ctx, x.org); err != nil || len(ps) != 1 {
		t.Errorf("Providers = %+v, %v", ps, err)
	}
	if n := trail(t, x, sso.ActionCreate); n != 1 {
		t.Errorf("creations on the trail = %d", n)
	}
	for _, c := range []struct {
		x    seeded
		kind sso.Kind
	}{{x, sso.SAML}, {other, sso.OIDC}} {
		got, err := s.CreateDefaultProvider(ctx, c.x.provider(c.kind, "p", t1), []byte("s"),
			byAdmin(c.x, sso.ActionCreate))
		if err != nil || !got.Default {
			t.Errorf("a %s default in %s = %+v, %v", c.kind, c.x.org, got, err)
		}
	}
}

// TestAccIdentityProviderContract holds the schema to one default per kind
// in an organization and to identities naming a provider of their own
// organization.
func TestAccIdentityProviderContract(t *testing.T) {
	ctx := t.Context()
	db := pool(t)
	x := seed(t, db, "raviga")
	other := seed(t, db, "bachmanity")
	insert := func(org kernel.OrganizationID, kind string, def bool) error {
		_, err := db.Exec(ctx, `INSERT INTO identity_providers (organization_id, id, kind, name,
			settings, secret_sealed, is_default) VALUES ($1, $2, $3, 'p', '{}', '\x00', $4)`,
			org, kernel.NewULID(), kind, def)
		return err
	}
	code := func(err error) string {
		if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
			return pe.Code
		}
		return ""
	}
	for _, c := range []struct {
		org  kernel.OrganizationID
		kind string
		def  bool
		want string
	}{
		{x.org, "OIDC", true, ""},
		{x.org, "OIDC", false, ""},
		{x.org, "OIDC", true, "23505"},
		{x.org, "SAML", true, ""},
		{other.org, "OIDC", true, ""},
		{x.org, "LDAP", false, "23514"},
	} {
		if got := code(insert(c.org, c.kind, c.def)); got != c.want {
			t.Errorf("%s default %t in %s = %q, want %q", c.kind, c.def, c.org, got, c.want)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO identity_providers (organization_id, id, kind, name,
		settings, secret_sealed, enabled_at) VALUES ($1, $2, 'OIDC', 'p', '{}', '\x00', now())`,
		x.org, kernel.NewULID()); code(err) != "23514" {
		t.Errorf("enabled with no admin login = %v", err)
	}
	identity := func(org kernel.OrganizationID, member kernel.MemberID, provider kernel.ULID) error {
		_, err := db.Exec(ctx, `INSERT INTO member_identities (organization_id, member_id,
			identity_provider_id, subject) VALUES ($1, $2, $3, $4)`,
			org, member, provider, kernel.NewULID().String())
		return err
	}
	if err := identity(x.org, x.member, kernel.NewULID()); code(err) != "23503" {
		t.Errorf("an identity of an unknown provider = %v", err)
	}
	if err := identity(x.org, x.member, seedProvider(t, db, other.org)); code(err) != "23503" {
		t.Errorf("an identity of another organization's provider = %v", err)
	}
	if err := identity(x.org, x.member, seedProvider(t, db, x.org)); err != nil {
		t.Errorf("an identity of the organization's provider = %v", err)
	}
}

func TestAccIdentityProvidersUnavailable(t *testing.T) {
	p := pool(t)
	s := pg.New(p).WithSealer(keyring(t, p))
	p.Close()
	ctx := context.Background()
	org, id := kernel.NewOrganizationID(), kernel.NewULID()
	e := kernel.OrganizationAuditEntry{OrganizationID: org}
	pr := sso.Provider{OrganizationID: org, ID: id, Kind: sso.OIDC}
	for name, call := range map[string]func() error{
		"providers": func() error { _, err := s.Providers(ctx, org); return err },
		"provider":  func() error { _, err := s.Provider(ctx, org, id); return err },
		"secret":    func() error { _, err := s.ProviderSecret(ctx, org, id); return err },
		"create":    func() error { _, err := s.CreateProvider(ctx, pr, []byte("s"), e); return err },
		"create default": func() error {
			_, err := s.CreateDefaultProvider(ctx, pr, []byte("s"), e)
			return err
		},
		"update":      func() error { _, err := s.UpdateProvider(ctx, pr, []byte("s"), e); return err },
		"delete":      func() error { _, _, err := s.DeleteProvider(ctx, org, id, e); return err },
		"set default": func() error { _, err := s.SetDefaultProvider(ctx, org, id, e); return err },
	} {
		if err := call(); err == nil || errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s on a closed pool = %v", name, err)
		}
	}
}
