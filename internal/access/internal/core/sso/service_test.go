package sso_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	featuresapi "github.com/weaveplatform/infraweave/internal/features/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var (
	t0      = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	errDown = errors.New("down")
)

// store is sso.Store over one organization's providers, applying the
// default rules as the pg store does. secrets are what each write sealed;
// trail each write's entry.
type store struct {
	ps      []sso.Provider
	secrets map[kernel.ULID][]byte
	trail   []kernel.OrganizationAuditEntry
	revoked []kernel.ULID
	fail    bool
}

func (s *store) Providers(context.Context, kernel.OrganizationID) ([]sso.Provider, error) {
	if s.fail {
		return nil, errDown
	}
	return slices.Clone(s.ps), nil
}

func (s *store) find(id kernel.ULID) (int, error) {
	i := slices.IndexFunc(s.ps, func(p sso.Provider) bool { return p.ID == id })
	if i < 0 {
		return 0, port.ErrNotFound
	}
	return i, nil
}

func (s *store) Provider(_ context.Context, _ kernel.OrganizationID, id kernel.ULID) (sso.Provider, error) {
	i, err := s.find(id)
	if err != nil {
		return sso.Provider{}, err
	}
	return s.ps[i], nil
}

func (s *store) CreateProvider(_ context.Context, p sso.Provider, secret []byte, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	p.Default = sso.FirstOfKind(s.ps, p.Kind)
	s.ps = append(s.ps, p)
	s.secrets[p.ID] = secret
	s.trail = append(s.trail, e)
	return p, nil
}

func (s *store) CreateDefaultProvider(ctx context.Context, p sso.Provider, secret []byte, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	if !sso.FirstOfKind(s.ps, p.Kind) {
		return sso.Provider{}, sso.ErrDefaultExists
	}
	return s.CreateProvider(ctx, p, secret, e)
}

func (s *store) UpdateProvider(_ context.Context, p sso.Provider, secret []byte, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	i, err := s.find(p.ID)
	if err != nil {
		return sso.Provider{}, err
	}
	s.ps[i] = p
	if secret != nil {
		s.secrets[p.ID] = secret
	}
	s.trail = append(s.trail, e)
	return p, nil
}

func (s *store) DeleteProvider(_ context.Context, _ kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (sso.Provider, []kernel.ULID, error) {
	i, err := s.find(id)
	if err != nil {
		return sso.Provider{}, nil, err
	}
	gone := s.ps[i]
	s.ps = slices.Delete(s.ps, i, i+1)
	if next, ok := sso.Successor(s.ps, gone.Kind); ok && gone.Default {
		j, _ := s.find(next.ID)
		s.ps[j].Default = true
	}
	s.trail = append(s.trail, e)
	return gone, s.revoked, nil
}

func (s *store) SetDefaultProvider(_ context.Context, _ kernel.OrganizationID, id kernel.ULID, e kernel.OrganizationAuditEntry) (sso.Provider, error) {
	i, err := s.find(id)
	if err != nil {
		return sso.Provider{}, err
	}
	for j := range s.ps {
		if s.ps[j].Kind == s.ps[i].Kind {
			s.ps[j].Default = j == i
		}
	}
	s.trail = append(s.trail, e)
	return s.ps[i], nil
}

// admins admit every principal but a member that is not admin.
type admins struct{ admin kernel.MemberID }

func (a admins) Admit(_ context.Context, by orgadmin.Actor) error {
	if by.Principal.Kind == kernel.PrincipalUser && by.Principal.MemberID != a.admin {
		return orgadmin.ErrForbidden
	}
	return nil
}

// features refuse the features in refused.
type features struct{ refused []string }

func (f features) AllowAccount(_ context.Context, _ kernel.OrganizationID, _ kernel.AccountID, feature string) error {
	if slices.Contains(f.refused, feature) {
		return &featuresapi.Refusal{Feature: feature, Tiers: []string{"Enterprise"}}
	}
	return nil
}

// discovery reads a discovery document of every host but the ones in
// refused, recording each host it is asked for.
type discovery struct {
	refused map[string]error
	asked   []string
}

func (d *discovery) Discover(_ context.Context, host string) (sso.Endpoints, error) {
	d.asked = append(d.asked, host)
	if err := d.refused[host]; err != nil {
		return sso.Endpoints{}, err
	}
	return sso.Endpoints{Issuer: host}, nil
}

type world struct {
	st      *store
	s       *sso.Service
	disc    *discovery
	admin   orgadmin.Actor
	member  orgadmin.Actor
	dropped []kernel.ULID
}

func newWorld(f featuresapi.Options) *world {
	org := kernel.NewOrganizationID()
	alice, bob := kernel.NewMemberID(), kernel.NewMemberID()
	w := &world{st: &store{secrets: map[kernel.ULID][]byte{}}, disc: &discovery{refused: map[string]error{}}}
	actor := func(m kernel.MemberID) orgadmin.Actor {
		return orgadmin.Actor{Principal: kernel.Principal{
			Kind: kernel.PrincipalUser, OrganizationID: org, MemberID: m, ActorID: m.ULID,
		}, RemoteIP: "192.0.2.1"}
	}
	w.admin, w.member = actor(alice), actor(bob)
	w.s = sso.NewService(sso.Deps{
		Store: w.st, Admins: admins{admin: alice}, Features: f, Discovery: w.disc,
		Drop: func(id kernel.ULID) { w.dropped = append(w.dropped, id) },
		Now:  func() time.Time { return t0 },
	})
	return w
}

func oidcInput(name string) sso.Input {
	return sso.Input{
		Kind: "OIDC", Name: name, Secret: ptr("s3cret"),
		Settings: sso.SettingsInput{IdentityProviderHost: ptr("https://login.example.com"), ClientID: ptr("web")},
	}
}

// TestUnitCreate creates a provider of each kind, disabled and the first
// of its kind its default, sealing the client secret or the generated SAML
// key and recording no secret on the trail.
func TestUnitCreate(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	first, err := w.s.Create(ctx, w.admin, oidcInput("Okta"))
	if err != nil || !first.Default || first.Enabled() || first.Name != "Okta" || !first.CreatedAt.Equal(t0) {
		t.Fatalf("Create = %+v, %v", first, err)
	}
	second, err := w.s.Create(ctx, w.admin, oidcInput("Entra ID"))
	if err != nil || second.Default {
		t.Errorf("a second OIDC provider = %+v, %v", second, err)
	}
	saml, err := w.s.Create(ctx, w.admin, sso.Input{
		Kind: "SAML", Name: "ADFS",
		Settings: sso.SettingsInput{IDPMetadataURL: ptr("https://adfs.example/md"), NameIDFormat: ptr("PERSISTENT")},
	})
	if err != nil || !saml.Default {
		t.Fatalf("SAML = %+v, %v", saml, err)
	}
	if _, err := x509.ParsePKCS8PrivateKey(w.st.secrets[saml.ID]); err != nil {
		t.Errorf("the SAML secret is no key: %v", err)
	}
	github, err := w.s.Create(ctx, w.admin, sso.Input{
		Kind: "GITHUB_OAUTH", Name: "GitHub", Secret: ptr("gh"),
		Settings: sso.SettingsInput{ClientID: ptr("Iv1"), Organization: ptr("acme")},
	})
	if err != nil || !github.Default || github.Settings.BaseURL != sso.GitHubURL {
		t.Errorf("GitHub = %+v, %v", github, err)
	}
	if string(w.st.secrets[first.ID]) != "s3cret" || string(w.st.secrets[github.ID]) != "gh" {
		t.Errorf("secrets = %q", w.st.secrets)
	}
	if len(w.st.trail) != 4 {
		t.Fatalf("trail = %+v", w.st.trail)
	}
	e := w.st.trail[0]
	if e.Action != sso.ActionCreate || e.EventType != "CREATE" || e.AffectedResource.Type != sso.ResourceProvider ||
		e.AffectedResource.ID != first.ID.String() || e.RemoteIP != "192.0.2.1" || e.ActorKind != kernel.PrincipalUser {
		t.Errorf("entry = %+v", e)
	}
	for _, e := range w.st.trail {
		if strings.Contains(string(e.Body), "s3cret") || strings.Contains(string(e.Body), "gh\"") {
			t.Errorf("the trail records a secret: %s", e.Body)
		}
	}
	if ps, err := w.s.List(ctx, w.admin); err != nil || len(ps) != 4 {
		t.Errorf("List = %d, %v", len(ps), err)
	}
}

// TestUnitCreateRefused refuses invalid input with a FieldError per field,
// a kind whose feature the organization lacks, and a member without the
// organization role.
func TestUnitCreateRefused(t *testing.T) {
	w := newWorld(features{refused: []string{"saml-sso"}})
	ctx := t.Context()
	for name, c := range map[string]struct {
		in     sso.Input
		fields []string
	}{
		"an unknown kind": {in: sso.Input{Kind: "LDAP", Name: "x"}, fields: []string{"kind"}},
		"no name, no secret": {
			in: sso.Input{Kind: "OIDC", Settings: oidcInput("").Settings}, fields: []string{"name", "secret"},
		},
		"an empty secret": {
			in: func() sso.Input { in := oidcInput("x"); in.Secret = ptr(" "); return in }(), fields: []string{"secret"},
		},
		"a setting of another kind": {
			in: func() sso.Input {
				in := oidcInput("x")
				in.Settings.BaseURL = ptr("https://github.com")
				return in
			}(),
			fields: []string{"settings.baseUrl"},
		},
		"a github secret, and no organization": {
			in: sso.Input{
				Kind: "GITHUB_OAUTH", Name: "gh", Settings: sso.SettingsInput{ClientID: ptr("c")},
			},
			fields: []string{"settings.organization", "secret"},
		},
	} {
		_, err := w.s.Create(ctx, w.admin, c.in)
		if diff := cmp.Diff(c.fields, fieldsOf(err)); diff != "" || !errors.Is(err, organization.ErrInvalid) {
			t.Errorf("%s: fields (-want +got):\n%s\n%v", name, diff, err)
		}
	}
	_, err := w.s.Create(ctx, w.admin, sso.Input{
		Kind: "SAML", Name: "ADFS", Secret: ptr("x"),
		Settings: sso.SettingsInput{IDPMetadataURL: ptr("https://adfs.example/md")},
	})
	if r, ok := errors.AsType[*featuresapi.Refusal](err); !ok || r.Feature != "saml-sso" {
		t.Errorf("SAML without saml-sso = %v", err)
	}
	if _, err := w.s.Create(ctx, w.member, oidcInput("x")); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("a member = %v", err)
	}
	if _, err := w.s.List(ctx, w.member); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("a member's list = %v", err)
	}
	if len(w.st.ps) != 0 {
		t.Errorf("refused creations stored %+v", w.st.ps)
	}
	w.st.fail = true
	if _, err := w.s.List(ctx, w.admin); !errors.Is(err, errDown) {
		t.Errorf("an unreadable list = %v", err)
	}
}

// TestUnitSAMLSecret refuses a secret for a SAML provider, whose key is
// generated, at creation and at an update.
func TestUnitSAMLSecret(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	in := sso.Input{
		Kind: "SAML", Name: "ADFS", Secret: ptr("key"),
		Settings: sso.SettingsInput{IDPMetadataURL: ptr("https://adfs.example/md"), NameIDFormat: ptr("PERSISTENT")},
	}
	if _, err := w.s.Create(ctx, w.admin, in); !slices.Equal(fieldsOf(err), []string{"secret"}) {
		t.Errorf("a SAML secret = %v", err)
	}
	in.Secret = nil
	p, err := w.s.Create(ctx, w.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.Update(ctx, w.admin, p.ID.String(), sso.Patch{Secret: ptr("key")}); !slices.Equal(fieldsOf(err), []string{"secret"}) {
		t.Errorf("a SAML secret's update = %v", err)
	}
}

// TestUnitUpdate changes the name, claim mapping and secret, records the
// change without the secret, and refuses the kind and an immutable
// setting.
func TestUnitUpdate(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	p, err := w.s.Create(ctx, w.admin, oidcInput("Okta"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.s.Update(ctx, w.admin, p.ID.String(), sso.Patch{
		Kind: ptr("OIDC"), Name: ptr("Okta EU"), Secret: ptr("rotated"),
		Settings: &sso.SettingsInput{ClaimMapping: &[]sso.ClaimMappingEntry{{Name: "groups", Value: "roles"}}},
	})
	if err != nil || got.Name != "Okta EU" || len(got.Settings.ClaimMapping) != 1 || !got.Default {
		t.Fatalf("Update = %+v, %v", got, err)
	}
	if string(w.st.secrets[p.ID]) != "rotated" {
		t.Errorf("secret = %q", w.st.secrets[p.ID])
	}
	e := w.st.trail[len(w.st.trail)-1]
	var b map[string]any
	if err := json.Unmarshal(e.Body, &b); err != nil || e.Action != sso.ActionUpdate || b["secret"] != "changed" ||
		b["name"] != "Okta EU" || strings.Contains(string(e.Body), "rotated") {
		t.Errorf("entry = %+v, %s", e, e.Body)
	}
	if _, err := w.s.Update(ctx, w.admin, p.ID.String(), sso.Patch{}); err != nil {
		t.Errorf("an empty patch = %v", err)
	}
	if string(w.st.secrets[p.ID]) != "rotated" {
		t.Errorf("an empty patch changed the secret to %q", w.st.secrets[p.ID])
	}
	_, err = w.s.Update(ctx, w.admin, p.ID.String(), sso.Patch{
		Kind: ptr("SAML"), Name: ptr(""), Secret: ptr(""),
		Settings: &sso.SettingsInput{ClientID: ptr("other")},
	})
	if want := []string{"kind", "name", "settings.clientId", "secret"}; !slices.Equal(fieldsOf(err), want) {
		t.Errorf("an immutable patch = %v, want %v", fieldsOf(err), want)
	}
	for name, id := range map[string]string{
		"no ID": "nope", "an unknown provider": kernel.NewULID().String(),
	} {
		if _, err := w.s.Update(ctx, w.admin, id, sso.Patch{}); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if _, err := w.s.Update(ctx, w.member, p.ID.String(), sso.Patch{}); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("a member = %v", err)
	}
}

// TestUnitDeleteAndDefault chooses another default of a kind, and deleting
// the default promotes the oldest remaining provider and drops the
// sessions the store revoked.
func TestUnitDeleteAndDefault(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	ids := make([]string, 0, 3)
	for _, name := range []string{"a", "b", "c"} {
		p, err := w.s.Create(ctx, w.admin, oidcInput(name))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID.String())
	}
	p, err := w.s.SetDefault(ctx, w.admin, ids[2])
	if err != nil || !p.Default {
		t.Fatalf("SetDefault = %+v, %v", p, err)
	}
	if e := w.st.trail[len(w.st.trail)-1]; e.Action != sso.ActionSetDefault {
		t.Errorf("entry = %+v", e)
	}
	session := kernel.NewULID()
	w.st.revoked = []kernel.ULID{session}
	gone, err := w.s.Delete(ctx, w.admin, ids[2])
	if err != nil || gone.ID.String() != ids[2] || !slices.Equal(w.dropped, []kernel.ULID{session}) {
		t.Fatalf("Delete = %+v, %v, dropped %v", gone, err, w.dropped)
	}
	if e := w.st.trail[len(w.st.trail)-1]; e.Action != sso.ActionDelete || e.EventType != "DELETE" {
		t.Errorf("entry = %+v", e)
	}
	if !w.st.ps[0].Default || w.st.ps[1].Default {
		t.Errorf("after deleting the default: %+v", w.st.ps)
	}
	for name, call := range map[string]func(string) error{
		"delete":      func(id string) error { _, err := w.s.Delete(ctx, w.admin, id); return err },
		"set default": func(id string) error { _, err := w.s.SetDefault(ctx, w.admin, id); return err },
	} {
		if err := call(kernel.NewULID().String()); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s of an unknown provider = %v", name, err)
		}
		if err := call("nope"); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s of no ID = %v", name, err)
		}
	}
	if _, err := w.s.Delete(ctx, w.member, ids[0]); !errors.Is(err, orgadmin.ErrForbidden) {
		t.Errorf("a member's delete = %v", err)
	}
}
