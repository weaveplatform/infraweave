package sso_test

import (
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func ptr[T any](v T) *T { return &v }

const metadata = `<?xml version="1.0"?>
<md:EntityDescriptor xmlns:md="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">
  <md:IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol"/>
</md:EntityDescriptor>`

func subject(attr string) *[]sso.ClaimMappingEntry {
	return &[]sso.ClaimMappingEntry{{Name: "subject", Value: attr}}
}

// fieldsOf are the fields err's FieldErrors name, in order.
func fieldsOf(err error) []string {
	var out []string
	var walk func(error)
	walk = func(err error) {
		if fe, ok := err.(*organization.FieldError); ok { //nolint:errorlint // walks the join by hand
			out = append(out, fe.Field)
			return
		}
		if j, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // as above
			for _, e := range j.Unwrap() {
				walk(e)
			}
		}
	}
	walk(err)
	return out
}

func TestUnitParseKind(t *testing.T) {
	for _, k := range []sso.Kind{sso.OIDC, sso.SAML, sso.GitHubOAuth} {
		if got, err := sso.ParseKind(string(k)); err != nil || got != k {
			t.Errorf("ParseKind(%s) = %s, %v", k, got, err)
		}
	}
	if _, err := sso.ParseKind("LDAP"); !errors.Is(err, organization.ErrInvalid) ||
		!strings.HasPrefix(err.Error(), "kind:") {
		t.Errorf("ParseKind(LDAP) = %v", err)
	}
	for k, want := range map[sso.Kind]string{
		sso.OIDC: "oidc-sso", sso.SAML: "saml-sso", sso.GitHubOAuth: "organization",
	} {
		if got := k.Feature(); got != want {
			t.Errorf("%s's feature = %s, want %s", k, got, want)
		}
	}
}

// TestUnitNewSettings decodes each kind's settings strictly: a setting of
// another kind, and each rule a kind's settings break, is a FieldError
// naming the setting.
func TestUnitNewSettings(t *testing.T) {
	for name, c := range map[string]struct {
		kind   sso.Kind
		in     sso.SettingsInput
		want   sso.Settings
		fields []string
	}{
		"oidc": {
			kind: sso.OIDC,
			in:   sso.SettingsInput{IdentityProviderHost: ptr("https://login.example.com"), ClientID: ptr("web")},
			want: sso.Settings{IdentityProviderHost: "https://login.example.com", ClientID: "web"},
		},
		"oidc with a mapping": {
			kind: sso.OIDC,
			in: sso.SettingsInput{
				IdentityProviderHost: ptr("https://login.microsoftonline.com/t/v2.0"), ClientID: ptr("web"),
				ClaimMapping: subject("oid"),
			},
			want: sso.Settings{
				IdentityProviderHost: "https://login.microsoftonline.com/t/v2.0", ClientID: "web",
				ClaimMapping: []sso.ClaimMappingEntry{{Name: "subject", Value: "oid"}},
			},
		},
		"oidc with an empty mapping": {
			kind: sso.OIDC,
			in: sso.SettingsInput{
				IdentityProviderHost: ptr("https://a.example"), ClientID: ptr("web"),
				ClaimMapping: &[]sso.ClaimMappingEntry{},
			},
			want: sso.Settings{IdentityProviderHost: "https://a.example", ClientID: "web"},
		},
		"oidc over http, without a client": {
			kind:   sso.OIDC,
			in:     sso.SettingsInput{IdentityProviderHost: ptr("http://login.example.com"), ClientID: ptr(" ")},
			fields: []string{"settings.identityProviderHost", "settings.clientId"},
		},
		"oidc without a host": {
			kind:   sso.OIDC,
			in:     sso.SettingsInput{IdentityProviderHost: ptr("https:///path"), ClientID: ptr("web")},
			fields: []string{"settings.identityProviderHost"},
		},
		"oidc with a setting of saml": {
			kind: sso.OIDC,
			in: sso.SettingsInput{
				IdentityProviderHost: ptr("https://a.example"), ClientID: ptr("web"),
				NameIDFormat: ptr("PERSISTENT"), Organization: ptr("acme"),
			},
			fields: []string{"settings.nameIDFormat", "settings.organization"},
		},
		"saml metadata, transient by default": {
			kind: sso.SAML,
			in:   sso.SettingsInput{IDPMetadata: ptr(metadata), ClaimMapping: subject("uid")},
			want: sso.Settings{
				IDPMetadata: metadata, NameIDFormat: sso.NameIDTransient,
				ClaimMapping: []sso.ClaimMappingEntry{{Name: "subject", Value: "uid"}},
			},
		},
		"saml metadata url, persistent": {
			kind: sso.SAML,
			in:   sso.SettingsInput{IDPMetadataURL: ptr("https://idp.example/md"), NameIDFormat: ptr("PERSISTENT")},
			want: sso.Settings{IDPMetadataURL: "https://idp.example/md", NameIDFormat: sso.NameIDPersistent},
		},
		"saml with both": {
			kind: sso.SAML,
			in: sso.SettingsInput{
				IDPMetadata: ptr(metadata), IDPMetadataURL: ptr("https://idp.example/md"),
				ClaimMapping: subject("uid"),
			},
			fields: []string{"settings.idpMetadata"},
		},
		"saml with neither": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{NameIDFormat: ptr("PERSISTENT")},
			fields: []string{"settings.idpMetadata"},
		},
		"saml metadata url over http": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadataURL: ptr("http://idp.example/md"), NameIDFormat: ptr("PERSISTENT")},
			fields: []string{"settings.idpMetadataURL"},
		},
		"saml metadata that is not xml": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr("<md:EntityDescriptor"), NameIDFormat: ptr("PERSISTENT")},
			fields: []string{"settings.idpMetadata"},
		},
		"saml metadata of another root": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr("<feed/>"), NameIDFormat: ptr("PERSISTENT")},
			fields: []string{"settings.idpMetadata"},
		},
		"saml metadata with no element": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr("<!-- none -->"), NameIDFormat: ptr("PERSISTENT")},
			fields: []string{"settings.idpMetadata"},
		},
		"saml e-mail without a subject": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr(metadata), NameIDFormat: ptr("EMAIL_ADDRESS")},
			fields: []string{"settings.claimMapping"},
		},
		"saml of an unknown format": {
			kind:   sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr(metadata), NameIDFormat: ptr("UNSPECIFIED"), ClaimMapping: subject("uid")},
			fields: []string{"settings.nameIDFormat"},
		},
		"github, github.com by default": {
			kind: sso.GitHubOAuth,
			in:   sso.SettingsInput{ClientID: ptr("Iv1.abc"), Organization: ptr("acme-corp")},
			want: sso.Settings{BaseURL: sso.GitHubURL, ClientID: "Iv1.abc", Organization: "acme-corp"},
		},
		"github enterprise server": {
			kind: sso.GitHubOAuth,
			in:   sso.SettingsInput{BaseURL: ptr("https://git.example.com/"), ClientID: ptr("c"), Organization: ptr("a")},
			want: sso.Settings{BaseURL: "https://git.example.com", ClientID: "c", Organization: "a"},
		},
		"github over http, of no organization": {
			kind:   sso.GitHubOAuth,
			in:     sso.SettingsInput{BaseURL: ptr("http://git.example.com"), ClientID: ptr("c"), Organization: ptr("-acme")},
			fields: []string{"settings.baseUrl", "settings.organization"},
		},
		"github with a mapping": {
			kind: sso.GitHubOAuth,
			in: sso.SettingsInput{
				ClientID: ptr("c"), Organization: ptr("a"), ClaimMapping: subject("id"),
			},
			fields: []string{"settings.claimMapping"},
		},
		"a mapping of an unknown target, a twice-mapped one and an empty claim": {
			kind: sso.OIDC,
			in: sso.SettingsInput{
				IdentityProviderHost: ptr("https://a.example"), ClientID: ptr("web"),
				ClaimMapping: &[]sso.ClaimMappingEntry{
					{Name: "teams", Value: "groups"},
					{Name: "login", Value: "upn"},
					{Name: "login", Value: "email"},
					{Name: "email", Value: " "},
				},
			},
			fields: []string{
				"settings.claimMapping.entries[0].name",
				"settings.claimMapping.entries[2].name",
				"settings.claimMapping.entries[3].value",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := sso.NewSettings(c.kind, c.in)
			if diff := cmp.Diff(c.fields, fieldsOf(err)); diff != "" {
				t.Errorf("fields (-want +got):\n%s\n%v", diff, err)
			}
			if c.fields != nil {
				if !errors.Is(err, organization.ErrInvalid) {
					t.Errorf("err = %v, want organization.ErrInvalid", err)
				}
				return
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("settings (-want +got):\n%s", diff)
			}
		})
	}
}

// TestUnitChange keeps the issuer, client ID, NameID format, base URL and
// GitHub organization: another value is refused, the same one is no
// change. The claim mapping and the SAML metadata change, either of the
// metadata replacing the other.
func TestUnitChange(t *testing.T) {
	oidc := sso.Settings{IdentityProviderHost: "https://a.example", ClientID: "web"}
	saml := sso.Settings{IDPMetadata: metadata, NameIDFormat: sso.NameIDPersistent}
	github := sso.Settings{BaseURL: sso.GitHubURL, ClientID: "c", Organization: "acme"}
	for name, c := range map[string]struct {
		from   sso.Settings
		kind   sso.Kind
		in     sso.SettingsInput
		want   sso.Settings
		fields []string
	}{
		"oidc's mapping": {
			from: oidc, kind: sso.OIDC, in: sso.SettingsInput{ClaimMapping: subject("oid")},
			want: sso.Settings{
				IdentityProviderHost: "https://a.example", ClientID: "web",
				ClaimMapping: []sso.ClaimMappingEntry{{Name: "subject", Value: "oid"}},
			},
		},
		"oidc's same issuer and client": {
			from: oidc, kind: sso.OIDC,
			in:   sso.SettingsInput{IdentityProviderHost: ptr("https://a.example"), ClientID: ptr("web")},
			want: oidc,
		},
		"oidc's issuer and client": {
			from: oidc, kind: sso.OIDC,
			in:     sso.SettingsInput{IdentityProviderHost: ptr("https://b.example"), ClientID: ptr("other")},
			fields: []string{"settings.identityProviderHost", "settings.clientId"},
		},
		"oidc given a saml setting": {
			from: oidc, kind: sso.OIDC, in: sso.SettingsInput{IDPMetadata: ptr(metadata)},
			fields: []string{"settings.idpMetadata"},
		},
		"saml's metadata to a url": {
			from: saml, kind: sso.SAML, in: sso.SettingsInput{IDPMetadataURL: ptr("https://idp.example/md")},
			want: sso.Settings{IDPMetadataURL: "https://idp.example/md", NameIDFormat: sso.NameIDPersistent},
		},
		"saml's url back to metadata": {
			from: sso.Settings{IDPMetadataURL: "https://idp.example/md", NameIDFormat: sso.NameIDPersistent},
			kind: sso.SAML, in: sso.SettingsInput{IDPMetadata: ptr(metadata)},
			want: saml,
		},
		"saml's format": {
			from: saml, kind: sso.SAML, in: sso.SettingsInput{NameIDFormat: ptr("TRANSIENT")},
			fields: []string{"settings.nameIDFormat"},
		},
		"saml's both metadata": {
			from: saml, kind: sso.SAML,
			in:     sso.SettingsInput{IDPMetadata: ptr(metadata), IDPMetadataURL: ptr("https://idp.example/md")},
			fields: []string{"settings.idpMetadata"},
		},
		"github's same base url": {
			from: github, kind: sso.GitHubOAuth, in: sso.SettingsInput{BaseURL: ptr("https://github.com/")},
			want: github,
		},
		"github's base url and organization": {
			from: github, kind: sso.GitHubOAuth,
			in:     sso.SettingsInput{BaseURL: ptr("https://git.example.com"), Organization: ptr("other")},
			fields: []string{"settings.baseUrl", "settings.organization"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := c.from.Change(c.kind, c.in)
			if diff := cmp.Diff(c.fields, fieldsOf(err)); diff != "" {
				t.Errorf("fields (-want +got):\n%s\n%v", diff, err)
			}
			if c.fields == nil {
				if diff := cmp.Diff(c.want, got); diff != "" {
					t.Errorf("settings (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestUnitClaims applies the claim mapping's defaults: an OIDC subject
// from sub, login from preferred_username, then email, then sub, and
// groups from groups, each entry replacing its target's.
func TestUnitClaims(t *testing.T) {
	want := map[sso.Target][]string{
		sso.TargetSubject: {"sub"},
		sso.TargetLogin:   {"preferred_username", "email", "sub"},
		sso.TargetName:    {"name"},
		sso.TargetEmail:   {"email"},
		sso.TargetGroups:  {"groups"},
	}
	if diff := cmp.Diff(want, sso.Settings{}.Claims(sso.OIDC)); diff != "" {
		t.Errorf("OIDC defaults (-want +got):\n%s", diff)
	}
	mapped := sso.Settings{ClaimMapping: []sso.ClaimMappingEntry{
		{Name: "subject", Value: "oid"}, {Name: "groups", Value: "roles"},
	}}
	want[sso.TargetSubject], want[sso.TargetGroups] = []string{"oid"}, []string{"roles"}
	if diff := cmp.Diff(want, mapped.Claims(sso.OIDC)); diff != "" {
		t.Errorf("Entra ID (-want +got):\n%s", diff)
	}
	saml := sso.Settings{}.Claims(sso.SAML)
	if _, ok := saml[sso.TargetSubject]; ok {
		t.Errorf("an unmapped SAML subject reads %v, want the NameID", saml[sso.TargetSubject])
	}
}

// TestUnitParseSettings refuses a column member Settings does not name.
func TestUnitParseSettings(t *testing.T) {
	s, err := sso.ParseSettings([]byte(`{"clientId":"web","claimMapping":[{"name":"login","value":"upn"}]}`))
	if err != nil || s.ClientID != "web" || len(s.ClaimMapping) != 1 {
		t.Errorf("ParseSettings = %+v, %v", s, err)
	}
	if _, err := sso.ParseSettings([]byte(`{"clientSecret":"x"}`)); err == nil {
		t.Error("an unknown member parsed")
	}
}

// TestUnitDefaultPerKind makes the first provider of a kind its default,
// and the oldest remaining one, the lower ID breaking a tie, when the
// default goes.
func TestUnitDefaultPerKind(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	a, b, c := kernel.NewULID(), kernel.NewULID(), kernel.NewULID()
	saml := sso.Provider{ID: a, Kind: sso.SAML, CreatedAt: t0}
	if !sso.FirstOfKind(nil, sso.OIDC) || !sso.FirstOfKind([]sso.Provider{saml}, sso.OIDC) ||
		sso.FirstOfKind([]sso.Provider{saml}, sso.SAML) {
		t.Error("FirstOfKind")
	}
	remaining := []sso.Provider{
		saml,
		{ID: c, Kind: sso.OIDC, CreatedAt: t0.Add(time.Minute)},
		{ID: b, Kind: sso.OIDC, CreatedAt: t0.Add(time.Minute)},
		{ID: kernel.NewULID(), Kind: sso.OIDC, CreatedAt: t0.Add(time.Hour)},
	}
	if next, ok := sso.Successor(remaining, sso.OIDC); !ok || next.ID != b {
		t.Errorf("Successor = %v, %t, want %v", next.ID, ok, b)
	}
	if _, ok := sso.Successor(remaining, sso.GitHubOAuth); ok {
		t.Error("a successor of a kind none remains of")
	}
}

// TestUnitNewSigningKey generates the SAML service provider's RSA-2048
// key, as the FIPS 140-3 module does under fips140=only.
func TestUnitNewSigningKey(t *testing.T) {
	der, err := sso.NewSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatal(err)
	}
	if rk, ok := k.(*rsa.PrivateKey); !ok || rk.N.BitLen() != sso.SigningKeyBits {
		t.Errorf("key %T", k)
	}
}

func TestUnitEnabled(t *testing.T) {
	if (sso.Provider{}).Enabled() || !(sso.Provider{EnabledAt: time.Now()}).Enabled() {
		t.Error("Enabled")
	}
}
