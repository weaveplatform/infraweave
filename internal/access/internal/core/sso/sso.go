package sso

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Kind is an identity provider's kind, which never changes.
type Kind string

// The kinds of identity provider.
const (
	OIDC        Kind = "OIDC"
	SAML        Kind = "SAML"
	GitHubOAuth Kind = "GITHUB_OAUTH"
)

// Feature is the catalogue feature creating a provider of k needs: a
// GitHub OAuth provider is the organization's own sign-in, so it needs
// only the Organization tag's.
//
// ref: decision 0021 § "The organization API"
func (k Kind) Feature() string {
	switch k {
	case OIDC:
		return "oidc-sso"
	case SAML:
		return "saml-sso"
	}
	return "organization"
}

// ParseKind refuses a kind that is none of the three as a FieldError
// naming kind.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if _, ok := fields[k]; !ok {
		return "", &organization.FieldError{Field: "kind", Reason: fmt.Sprintf("%q is no kind of identity provider", s)}
	}
	return k, nil
}

// NameIDFormat is the NameID format a SAML provider's service provider asks
// for.
type NameIDFormat string

// The NameID formats, TRANSIENT by default.
const (
	NameIDTransient  NameIDFormat = "TRANSIENT"
	NameIDEmail      NameIDFormat = "EMAIL_ADDRESS"
	NameIDPersistent NameIDFormat = "PERSISTENT"
)

// Target is what a claim mapping entry sets from the claim or attribute
// it names.
type Target string

// The claim mapping's targets.
const (
	TargetSubject Target = "subject"
	TargetLogin   Target = "login"
	TargetName    Target = "name"
	TargetEmail   Target = "email"
	TargetGroups  Target = "groups"
)

var targets = []Target{TargetSubject, TargetLogin, TargetName, TargetEmail, TargetGroups}

// ClaimMappingEntry maps the target Name to the claim or attribute Value.
type ClaimMappingEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GitHubURL is a GitHub OAuth provider's base URL when it names none.
const GitHubURL = "https://github.com"

// Settings are a provider's settings, which hold no secret. Each kind sets
// its own fields and no other.
type Settings struct {
	// IdentityProviderHost is an OIDC provider's https issuer or discovery
	// URL.
	IdentityProviderHost string `json:"identityProviderHost,omitempty"`
	// ClientID is an OIDC or GitHub OAuth provider's client ID.
	ClientID string `json:"clientId,omitempty"`
	// IDPMetadata and IDPMetadataURL are a SAML provider's metadata XML or
	// the https URL it is read from: exactly one of them.
	IDPMetadata    string       `json:"idpMetadata,omitempty"`
	IDPMetadataURL string       `json:"idpMetadataURL,omitempty"` //nolint:tagliatelle // the API's spelling, as Spacelift's
	NameIDFormat   NameIDFormat `json:"nameIDFormat,omitempty"`   //nolint:tagliatelle // as above
	// BaseURL is a GitHub OAuth provider's GitHub server.
	BaseURL string `json:"baseUrl,omitempty"`
	// Organization is the login of the GitHub organization whose members
	// sign in through a GitHub OAuth provider.
	Organization string `json:"organization,omitempty"`
	// ClaimMapping are an OIDC or SAML provider's entries, each replacing
	// its target's default.
	ClaimMapping []ClaimMappingEntry `json:"claimMapping,omitempty"`
}

// SettingsInput are the settings a request names, nil for one it leaves
// out.
type SettingsInput struct {
	IdentityProviderHost *string
	ClientID             *string
	IDPMetadata          *string
	IDPMetadataURL       *string
	NameIDFormat         *string
	BaseURL              *string
	Organization         *string
	ClaimMapping         *[]ClaimMappingEntry
}

// The settings' names, as the API and the settings column name them.
const (
	fieldHost         = "identityProviderHost"
	fieldClientID     = "clientId"
	fieldMetadata     = "idpMetadata"
	fieldMetadataURL  = "idpMetadataURL"
	fieldNameID       = "nameIDFormat"
	fieldBaseURL      = "baseUrl"
	fieldOrganization = "organization"
	fieldClaimMapping = "claimMapping"
)

// fields are the settings each kind takes.
var fields = map[Kind][]string{
	OIDC:        {fieldHost, fieldClientID, fieldClaimMapping},
	SAML:        {fieldMetadata, fieldMetadataURL, fieldNameID, fieldClaimMapping},
	GitHubOAuth: {fieldBaseURL, fieldClientID, fieldOrganization},
}

// immutable are the settings that never change: they name whom the
// provider trusts and what its subjects mean, so a changed one would sign
// existing identities in as other people. A change deletes the provider and
// creates another.
var immutable = []string{fieldHost, fieldClientID, fieldNameID, fieldBaseURL, fieldOrganization}

// named is each setting in input that is not nil, by name, as a string or
// the entries.
func (in SettingsInput) named() map[string]any {
	out := map[string]any{}
	for name, v := range map[string]*string{
		fieldHost: in.IdentityProviderHost, fieldClientID: in.ClientID,
		fieldMetadata: in.IDPMetadata, fieldMetadataURL: in.IDPMetadataURL,
		fieldNameID: in.NameIDFormat, fieldBaseURL: in.BaseURL, fieldOrganization: in.Organization,
	} {
		if v != nil {
			out[name] = *v
		}
	}
	if in.ClaimMapping != nil {
		out[fieldClaimMapping] = *in.ClaimMapping
	}
	return out
}

// value is the immutable setting name of s.
func (s Settings) value(name string) string {
	switch name {
	case fieldHost:
		return s.IdentityProviderHost
	case fieldClientID:
		return s.ClientID
	case fieldNameID:
		return string(s.NameIDFormat)
	case fieldBaseURL:
		return s.BaseURL
	}
	return s.Organization
}

// foreign refuses each setting in that kind k does not take.
func foreign(k Kind, in map[string]any) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(in)) {
		if !slices.Contains(fields[k], name) {
			errs = append(errs, &organization.FieldError{
				Field:  "settings." + name,
				Reason: fmt.Sprintf("is no setting of a %s provider", k),
			})
		}
	}
	return errors.Join(errs...)
}

// apply sets each setting of in on s. A SAML provider holds one of its
// metadata and its metadata URL, so setting either clears the other unless
// in sets both, which Check then refuses.
func (s Settings) apply(in SettingsInput) Settings {
	set := func(to *string, v *string) {
		if v != nil {
			*to = *v
		}
	}
	if in.IDPMetadata != nil && in.IDPMetadataURL == nil {
		s.IDPMetadataURL = ""
	}
	if in.IDPMetadataURL != nil && in.IDPMetadata == nil {
		s.IDPMetadata = ""
	}
	set(&s.IdentityProviderHost, in.IdentityProviderHost)
	set(&s.ClientID, in.ClientID)
	set(&s.IDPMetadata, in.IDPMetadata)
	set(&s.IDPMetadataURL, in.IDPMetadataURL)
	set(&s.BaseURL, in.BaseURL)
	set(&s.Organization, in.Organization)
	if in.NameIDFormat != nil {
		s.NameIDFormat = NameIDFormat(*in.NameIDFormat)
	}
	if in.ClaimMapping != nil {
		s.ClaimMapping = slices.Clone(*in.ClaimMapping)
	}
	return s
}

// withDefaults fills the settings kind k defaults: a SAML NameID format
// and a GitHub OAuth base URL.
func (s Settings) withDefaults(k Kind) Settings {
	switch k {
	case SAML:
		if s.NameIDFormat == "" {
			s.NameIDFormat = NameIDTransient
		}
	case GitHubOAuth:
		if s.BaseURL == "" {
			s.BaseURL = GitHubURL
		}
		s.BaseURL = strings.TrimSuffix(s.BaseURL, "/")
	}
	if len(s.ClaimMapping) == 0 {
		s.ClaimMapping = nil
	}
	return s
}

// NewSettings are the settings in names for a new provider of kind k, with
// k's defaults, or the FieldErrors of every setting k does not take or
// Check refuses.
func NewSettings(k Kind, in SettingsInput) (Settings, error) {
	if err := foreign(k, in.named()); err != nil {
		return Settings{}, err
	}
	s := Settings{}.apply(in).withDefaults(k)
	if err := s.Check(k); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Change is s, the settings of a provider of kind k, with in applied, or
// the FieldErrors of a setting k does not take, an immutable one in sets
// to another value, or what Check refuses of the result.
func (s Settings) Change(k Kind, in SettingsInput) (Settings, error) {
	named := in.named()
	if err := foreign(k, named); err != nil {
		return Settings{}, err
	}
	var errs []error
	for _, name := range immutable {
		v, ok := named[name].(string)
		if name == fieldBaseURL {
			v = strings.TrimSuffix(v, "/")
		}
		if !ok || v == s.value(name) {
			continue
		}
		errs = append(errs, &organization.FieldError{
			Field:  "settings." + name,
			Reason: "never changes; delete the provider and create another",
		})
	}
	if err := errors.Join(errs...); err != nil {
		return Settings{}, err
	}
	out := s.apply(in).withDefaults(k)
	if err := out.Check(k); err != nil {
		return Settings{}, err
	}
	return out, nil
}

// githubLogin is a GitHub organization's login: up to 39 letters, digits
// and single hyphens, neither first nor last.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)

// Check refuses settings a provider of kind k cannot hold, with a
// FieldError naming each setting: an OIDC provider's https issuer and
// client ID; a SAML provider's exactly one of metadata XML and an https
// metadata URL, and its NameID format; a GitHub OAuth provider's https base
// URL, client ID and organization login; and the claim mapping.
func (s Settings) Check(k Kind) error {
	var errs []error
	field := func(name, reason string) {
		errs = append(errs, &organization.FieldError{Field: "settings." + name, Reason: reason})
	}
	required := func(name, v string) {
		if strings.TrimSpace(v) == "" {
			field(name, "is required")
		}
	}
	https := func(name, v string) {
		if err := checkHTTPS(v); err != nil {
			field(name, err.Error())
		}
	}
	switch k {
	case OIDC:
		https(fieldHost, s.IdentityProviderHost)
		required(fieldClientID, s.ClientID)
	case SAML:
		switch {
		case s.IDPMetadata == "" && s.IDPMetadataURL == "":
			field(fieldMetadata, "or idpMetadataURL is required")
		case s.IDPMetadata != "" && s.IDPMetadataURL != "":
			field(fieldMetadata, "and idpMetadataURL are exclusive; set one")
		case s.IDPMetadata != "":
			if err := checkMetadata(s.IDPMetadata); err != nil {
				field(fieldMetadata, err.Error())
			}
		default:
			https(fieldMetadataURL, s.IDPMetadataURL)
		}
		switch s.NameIDFormat {
		case NameIDTransient, NameIDEmail, NameIDPersistent:
		default:
			field(fieldNameID, fmt.Sprintf("%q is none of TRANSIENT, EMAIL_ADDRESS and PERSISTENT", s.NameIDFormat))
		}
	case GitHubOAuth:
		https(fieldBaseURL, s.BaseURL)
		required(fieldClientID, s.ClientID)
		if !githubLogin.MatchString(s.Organization) {
			field(fieldOrganization, "is no GitHub organization login")
		}
	}
	errs = append(errs, s.checkMapping(k)...)
	return errors.Join(errs...)
}

// checkMapping refuses an entry naming no target or no claim, or a target
// a second time, and a SAML provider whose NameID is not persistent
// without a subject entry, since a transient or e-mail NameID does not
// identify one person over time.
func (s Settings) checkMapping(k Kind) []error {
	var errs []error
	seen := map[Target]bool{}
	for i, e := range s.ClaimMapping {
		at := fmt.Sprintf("settings.claimMapping.entries[%d]", i)
		t := Target(e.Name)
		switch {
		case !slices.Contains(targets, t):
			errs = append(errs, &organization.FieldError{
				Field:  at + ".name",
				Reason: fmt.Sprintf("%q is no claim target; one of subject, login, name, email and groups", e.Name),
			})
		case seen[t]:
			errs = append(errs, &organization.FieldError{Field: at + ".name", Reason: fmt.Sprintf("maps %s twice", t)})
		}
		seen[t] = true
		if strings.TrimSpace(e.Value) == "" {
			errs = append(errs, &organization.FieldError{Field: at + ".value", Reason: "is empty"})
		}
	}
	if k == SAML && s.NameIDFormat != NameIDPersistent && !seen[TargetSubject] {
		errs = append(errs, &organization.FieldError{
			Field:  "settings.claimMapping",
			Reason: fmt.Sprintf("maps no subject attribute, which a %s NameID needs", s.NameIDFormat),
		})
	}
	return errs
}

var (
	errNotHTTPS    = errors.New("is not an https URL")
	errNoHost      = errors.New("names no host")
	errNotMetadata = errors.New("is not SAML metadata: an EntityDescriptor or EntitiesDescriptor XML document")
)

func checkHTTPS(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Scheme != "https":
		return errNotHTTPS
	case u.Host == "":
		return errNoHost
	}
	return nil
}

// metadataNS is the SAML 2.0 metadata namespace.
const metadataNS = "urn:oasis:names:tc:SAML:2.0:metadata"

// checkMetadata refuses XML that is not well formed or whose root is not a
// SAML metadata EntityDescriptor or EntitiesDescriptor.
func checkMetadata(doc string) error {
	d := xml.NewDecoder(strings.NewReader(doc))
	var root *xml.StartElement
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errNotMetadata
		}
		if se, ok := tok.(xml.StartElement); ok && root == nil {
			root = &se
		}
	}
	if root == nil || root.Name.Space != metadataNS ||
		(root.Name.Local != "EntityDescriptor" && root.Name.Local != "EntitiesDescriptor") {
		return errNotMetadata
	}
	return nil
}

// ParseSettings decodes the settings column, refusing a member Settings
// does not name, so a column another version wrote is not read in part.
func ParseSettings(raw []byte) (Settings, error) {
	var s Settings
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("sso: settings: %w", err)
	}
	return s, nil
}

// defaultClaims are each target's claims when the mapping names none, read
// in order until one is present: a login falls back to the e-mail and then
// the subject, since not every provider sends preferred_username.
var defaultClaims = map[Target][]string{
	TargetLogin:  {"preferred_username", "email", "sub"},
	TargetName:   {"name"},
	TargetEmail:  {"email"},
	TargetGroups: {"groups"},
}

// Claims are the claims or attributes each target reads, in order until one
// is present: the mapping's entry, or the target's default. An OIDC
// subject reads sub unless mapped, as Entra ID's oid is; a SAML subject
// reads its mapped attribute, and under a persistent NameID without one,
// the NameID, which no claim names, so the target is absent.
func (s Settings) Claims(k Kind) map[Target][]string {
	out := make(map[Target][]string, len(targets))
	for t, claims := range defaultClaims {
		out[t] = slices.Clone(claims)
	}
	if k == OIDC {
		out[TargetSubject] = []string{"sub"}
	}
	for _, e := range s.ClaimMapping {
		out[Target(e.Name)] = []string{e.Value}
	}
	return out
}

// Provider is an identity provider of an organization. EnabledAt is zero,
// and AdminLogin empty, until a sign-in through it succeeds.
type Provider struct {
	OrganizationID kernel.OrganizationID
	ID             kernel.ULID
	Kind           Kind
	Name           string
	Settings       Settings
	Default        bool
	EnabledAt      time.Time
	AdminLogin     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Enabled reports whether a sign-in through p has succeeded.
func (p Provider) Enabled() bool { return !p.EnabledAt.IsZero() }

// FirstOfKind reports whether a new provider of kind k is its kind's
// default: it is when existing, the organization's providers, hold none of
// k.
//
// ref: decision 0006 § "Members and identity providers"
func FirstOfKind(existing []Provider, k Kind) bool {
	return !slices.ContainsFunc(existing, func(p Provider) bool { return p.Kind == k })
}

// Successor is the provider of kind k among remaining that becomes its
// kind's default when the default is deleted: the oldest, the lower ID
// breaking a tie. There is none when no provider of k remains.
func Successor(remaining []Provider, k Kind) (Provider, bool) {
	var (
		out   Provider
		found bool
	)
	for _, p := range remaining {
		if p.Kind != k {
			continue
		}
		if !found || p.CreatedAt.Before(out.CreatedAt) ||
			(p.CreatedAt.Equal(out.CreatedAt) && p.ID.String() < out.ID.String()) {
			out, found = p, true
		}
	}
	return out, found
}

// SigningKeyBits is a SAML provider's service provider signing key size,
// the smallest RSA key FIPS 140-3 approves for signing.
const SigningKeyBits = 2048

// NewSigningKey generates a SAML provider's service provider signing key,
// RSA-2048 from crypto/rsa, as PKCS #8 DER. crypto/rsa generates it in the
// FIPS 140-3 module under fips140=only.
func NewSigningKey() ([]byte, error) {
	k, err := rsa.GenerateKey(rand.Reader, SigningKeyBits)
	if err != nil {
		return nil, fmt.Errorf("sso: generate the signing key: %w", err)
	}
	return x509.MarshalPKCS8PrivateKey(k) //nolint:wrapcheck // an RSA key always marshals
}
