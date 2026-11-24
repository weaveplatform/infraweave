package sso_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func oidcSettingsInput() sso.OIDCInput {
	return sso.OIDCInput{
		IdentityProviderHost: "https://login.example.com", ClientID: "web", ClientCredentials: "s3cret",
		ClaimMapping: []sso.ClaimMappingEntry{{Name: "groups", Value: "roles"}},
	}
}

// TestUnitOIDCSettingsLifecycle creates the default OIDC provider through
// the SSO tag, disabled and named after its host, reads it, replaces its
// claim mapping, answers whether the organization has single sign-on as
// sign-ins enable providers, and deletes it, dropping its sessions.
func TestUnitOIDCSettingsLifecycle(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	if _, err := w.s.OIDCSettings(ctx, w.admin); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("no settings = %v", err)
	}
	p, err := w.s.CreateOIDC(ctx, w.admin, oidcSettingsInput())
	if err != nil || !p.Default || p.Enabled() || p.Name != "login.example.com" || p.Kind != sso.OIDC {
		t.Fatalf("CreateOIDC = %+v, %v", p, err)
	}
	if string(w.st.secrets[p.ID]) != "s3cret" || !slices.Equal(w.disc.asked, []string{"https://login.example.com"}) {
		t.Errorf("secret %q, discovery asked %v", w.st.secrets[p.ID], w.disc.asked)
	}
	if e := w.st.trail[0]; e.Action != sso.ActionCreate || strings.Contains(string(e.Body), "s3cret") {
		t.Errorf("entry = %+v, %s", e, e.Body)
	}
	got, err := w.s.OIDCSettings(ctx, w.admin)
	if err != nil || got.ID != p.ID {
		t.Errorf("OIDCSettings = %+v, %v", got, err)
	}
	for name, enable := range map[string]func(){
		"no provider enabled": func() {},
		"a GitHub OAuth provider enabled": func() {
			w.st.ps = append(w.st.ps, sso.Provider{ID: kernel.NewULID(), Kind: sso.GitHubOAuth, EnabledAt: t0})
		},
	} {
		enable()
		if on, err := w.s.HasSSO(ctx, w.admin); err != nil || on {
			t.Errorf("%s: HasSSO = %t, %v", name, on, err)
		}
	}
	w.st.ps[0].EnabledAt, w.st.ps[0].AdminLogin = t0, "ada"
	if on, err := w.s.HasSSO(ctx, w.admin); err != nil || !on {
		t.Errorf("an OIDC provider enabled: HasSSO = %t, %v", on, err)
	}

	got, err = w.s.UpdateOIDC(ctx, w.admin, []sso.ClaimMappingEntry{{Name: "subject", Value: "oid"}})
	if err != nil || !cmp.Equal(got.Settings.ClaimMapping, []sso.ClaimMappingEntry{{Name: "subject", Value: "oid"}}) ||
		!got.Enabled() || got.Settings.ClientID != "web" {
		t.Fatalf("UpdateOIDC = %+v, %v", got, err)
	}
	e := w.st.trail[len(w.st.trail)-1]
	var b map[string]any
	if err := json.Unmarshal(e.Body, &b); err != nil || e.Action != sso.ActionUpdate || b["settings"] == nil {
		t.Errorf("entry = %+v, %s", e, e.Body)
	}
	if got, err = w.s.UpdateOIDC(ctx, w.admin, nil); err != nil || got.Settings.ClaimMapping != nil {
		t.Errorf("an update without a mapping = %+v, %v", got.Settings, err)
	}

	session := kernel.NewULID()
	w.st.revoked = []kernel.ULID{session}
	gone, err := w.s.DeleteOIDC(ctx, w.admin)
	if err != nil || gone.ID != p.ID || !slices.Equal(w.dropped, []kernel.ULID{session}) {
		t.Fatalf("DeleteOIDC = %+v, %v, dropped %v", gone, err, w.dropped)
	}
	if on, err := w.s.HasSSO(ctx, w.admin); err != nil || on {
		t.Errorf("after the delete: HasSSO = %t, %v", on, err)
	}
	for name, call := range map[string]func() error{
		"read":   func() error { _, err := w.s.OIDCSettings(ctx, w.admin); return err },
		"update": func() error { _, err := w.s.UpdateOIDC(ctx, w.admin, nil); return err },
		"delete": func() error { _, err := w.s.DeleteOIDC(ctx, w.admin); return err },
	} {
		if err := call(); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("%s without settings = %v", name, err)
		}
	}
}

// TestUnitOIDCSettingsDeletePromotes deletes the default OIDC provider of
// several, which makes the oldest remaining one the default the SSO tag
// reads next.
func TestUnitOIDCSettingsDeletePromotes(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	first, err := w.s.CreateOIDC(ctx, w.admin, oidcSettingsInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.s.Create(ctx, w.admin, oidcInput("Entra ID"))
	if err != nil {
		t.Fatal(err)
	}
	if gone, err := w.s.DeleteOIDC(ctx, w.admin); err != nil || gone.ID != first.ID {
		t.Fatalf("DeleteOIDC = %+v, %v", gone, err)
	}
	if got, err := w.s.OIDCSettings(ctx, w.admin); err != nil || got.ID != second.ID {
		t.Errorf("the next default = %+v, %v", got, err)
	}
}

// TestUnitOIDCSettingsRefused refuses invalid input with FieldErrors named
// as the SSO tag's body names them, a second default OIDC provider, a host
// whose discovery fails, and a member without the organization role, and
// reaches the provider only for input it would store.
func TestUnitOIDCSettingsRefused(t *testing.T) {
	w := newWorld(nil)
	ctx := t.Context()
	for name, c := range map[string]struct {
		in     func(*sso.OIDCInput)
		fields []string
	}{
		"http, no client, no secret": {
			in: func(in *sso.OIDCInput) {
				in.IdentityProviderHost, in.ClientID, in.ClientCredentials = "http://login.example.com", "", ""
			},
			fields: []string{"identityProviderHost", "clientId", "clientCredentials"},
		},
		"an unknown claim target": {
			in:     func(in *sso.OIDCInput) { in.ClaimMapping = []sso.ClaimMappingEntry{{Name: "teams", Value: "groups"}} },
			fields: []string{"claimMapping.entries[0].name"},
		},
	} {
		in := oidcSettingsInput()
		c.in(&in)
		_, err := w.s.CreateOIDC(ctx, w.admin, in)
		if diff := cmp.Diff(c.fields, fieldsOf(err)); diff != "" || !errors.Is(err, organization.ErrInvalid) {
			t.Errorf("%s: fields (-want +got):\n%s\n%v", name, diff, err)
		}
	}
	unusable := oidcSettingsInput()
	unusable.IdentityProviderHost = "https://elsewhere.example.com"
	w.disc.refused[unusable.IdentityProviderHost] = errDown
	if _, err := w.s.CreateOIDC(ctx, w.admin, unusable); !slices.Equal(fieldsOf(err), []string{"identityProviderHost"}) ||
		!strings.Contains(err.Error(), "down") {
		t.Errorf("an unusable discovery document = %v", err)
	}
	if !slices.Equal(w.disc.asked, []string{unusable.IdentityProviderHost}) || len(w.st.ps) != 0 {
		t.Errorf("discovery asked %v, stored %+v", w.disc.asked, w.st.ps)
	}
	if _, err := w.s.CreateOIDC(ctx, w.admin, oidcSettingsInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.s.CreateOIDC(ctx, w.admin, oidcSettingsInput()); !errors.Is(err, sso.ErrDefaultExists) {
		t.Errorf("a second default = %v", err)
	}
	if len(w.disc.asked) != 2 {
		t.Errorf("a refused second default read discovery: %v", w.disc.asked)
	}
	_, err := w.s.UpdateOIDC(ctx, w.admin, []sso.ClaimMappingEntry{{Name: "groups", Value: " "}})
	if !slices.Equal(fieldsOf(err), []string{"claimMapping.entries[0].value"}) {
		t.Errorf("an empty claim = %v", err)
	}
	for name, call := range map[string]func() error{
		"read":   func() error { _, err := w.s.OIDCSettings(ctx, w.member); return err },
		"create": func() error { _, err := w.s.CreateOIDC(ctx, w.member, oidcSettingsInput()); return err },
		"update": func() error { _, err := w.s.UpdateOIDC(ctx, w.member, nil); return err },
		"delete": func() error { _, err := w.s.DeleteOIDC(ctx, w.member); return err },
		"has":    func() error { _, err := w.s.HasSSO(ctx, w.member); return err },
	} {
		if err := call(); !errors.Is(err, orgadmin.ErrForbidden) {
			t.Errorf("a member's %s = %v", name, err)
		}
	}
	w.st.fail = true
	for name, call := range map[string]func() error{
		"read":   func() error { _, err := w.s.OIDCSettings(ctx, w.admin); return err },
		"create": func() error { _, err := w.s.CreateOIDC(ctx, w.admin, oidcSettingsInput()); return err },
		"has":    func() error { _, err := w.s.HasSSO(ctx, w.admin); return err },
	} {
		if err := call(); !errors.Is(err, errDown) {
			t.Errorf("an unreadable %s = %v", name, err)
		}
	}
}

// TestUnitOIDCSettingsName cuts a host longer than a display name to one.
func TestUnitOIDCSettingsName(t *testing.T) {
	w := newWorld(nil)
	in := oidcSettingsInput()
	in.IdentityProviderHost = "https://" + strings.Repeat("a", 120) + ".example.com/tenant"
	p, err := w.s.CreateOIDC(t.Context(), w.admin, in)
	if err != nil || len(p.Name) != organization.MaxName || organization.CheckName("name", p.Name) != nil {
		t.Errorf("CreateOIDC = %q, %v", p.Name, err)
	}
}
