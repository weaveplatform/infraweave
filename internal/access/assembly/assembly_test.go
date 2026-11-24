package assembly

import (
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitAssembly(t *testing.T) {
	if s := Stream(); s.Name != "access" || s.Files == nil {
		t.Errorf("Stream = %+v", s)
	}
	if len(SealedColumns()) == 0 {
		t.Error("the access stream seals signing keys, but lists no sealed column")
	}
	d, err := New(Deps{Settings: DefaultSettings(), PublicURL: "https://iw.example.com"})
	if err != nil || d == nil {
		t.Fatalf("New = %v, %v", d, err)
	}
	_ = d.API()
	_ = (*Domain)(nil).API()
	_ = d.OperatorAPI()
	_ = (*Domain)(nil).OperatorAPI()
	if d.Authenticator() == nil || d.OperatorAuthenticator() == nil ||
		d.WorkerAuthenticator() == nil || d.StateAuthenticator() == nil {
		t.Error("no authenticator")
	}
	if s := DefaultSettings().Session; s.IdleTimeout != 10*time.Hour || s.MaxAge != 10*time.Hour {
		t.Errorf("session defaults = %+v, want 10h idle and absolute", s)
	}
	if tk := DefaultSettings().Tokens; tk.OAuthAccess.TTL != time.Hour ||
		tk.OAuthRefresh.TTL != 30*24*time.Hour || tk.OAuthCode.TTL != time.Minute {
		t.Errorf("OAuth defaults = %+v, want an hour, 30 days and a minute", tk)
	}
	// The login and the authorization server's metadata are mounted, and
	// a nil domain's answer 501.
	for _, path := range []string{"/admin-login/acme", "/.well-known/oauth-authorization-server"} {
		for dom, want := range map[*Domain]int{d: http.StatusOK, nil: http.StatusNotImplemented} {
			r := chi.NewRouter()
			dom.Routes(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
			if rec.Code != want {
				t.Errorf("GET %s on %v: %d, want %d", path, dom == nil, rec.Code, want)
			}
		}
	}
	// The federation issuer's documents are mounted for GET and HEAD at
	// the root, and under the issuer's path when it has one.
	if got := (*Domain)(nil).FederationDiscoveryPatterns(); strings.Join(got, " ") !=
		"/.well-known/openid-configuration /.well-known/jwks" {
		t.Errorf("a nil domain's discovery patterns: %v", got)
	}
	if got := d.FederationDiscoveryPatterns(); len(got) != 2 {
		t.Errorf("the public URL's discovery patterns: %v", got)
	}
	pathed := DefaultSettings()
	pathed.Federation.Issuer = "https://iw.example.com/federation/"
	if err := pathed.Validate(); err != nil {
		t.Fatalf("an issuer with a path: %v", err)
	}
	withPath := mustNew(t, Deps{Settings: pathed, PublicURL: "https://iw.example.com"})
	patterns := withPath.FederationDiscoveryPatterns()
	if strings.Join(patterns, " ") != "/.well-known/openid-configuration /.well-known/jwks "+
		"/federation/.well-known/openid-configuration /federation/.well-known/jwks" {
		t.Errorf("an issuer with a path's discovery patterns: %v", patterns)
	}
	for _, p := range patterns {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := chi.NewRouter()
			withPath.Routes(r)
			if !r.Match(chi.NewRouteContext(), method, p) {
				t.Errorf("%s %s is not mounted", method, p)
			}
		}
		r := chi.NewRouter()
		(*Domain)(nil).Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, p, nil))
		if want := http.StatusNotImplemented; strings.HasPrefix(p, "/.well-known") && rec.Code != want {
			t.Errorf("GET %s on a nil domain: %d, want %d", p, rec.Code, want)
		}
	}
	tasks := d.Cron()
	if len(tasks) != 2 || tasks[0].Kind.Name != "authn.sessions.expire" ||
		tasks[0].Every != time.Hour || tasks[1].Kind.Name != "authn.keys.rotate" ||
		tasks[1].Every != 24*time.Hour {
		t.Errorf("Cron = %+v, want authn.sessions.expire hourly and authn.keys.rotate daily", tasks)
	}
	manual := DefaultSettings()
	manual.Signing.Rotation = RotationManual
	manual.APIIssuer = "https://issuer.example.com"
	if tasks := mustNew(t, Deps{Settings: manual, Now: time.Now}).Cron(); len(tasks) != 1 ||
		tasks[0].Kind != ExpireSessions {
		t.Errorf("manual rotation registers %+v", tasks)
	}
}

// TestUnitAssemblyCAFile checks that auth.oidc_api_keys.ca_file is read
// at construction: a file that is missing, or holds no certificate, fails
// New naming the setting, and a PEM certificate is trusted.
func TestUnitAssemblyCAFile(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	good := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: srv.Certificate().Raw,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for file, ok := range map[string]bool{
		good:                             true,
		junk:                             false,
		filepath.Join(dir, "absent.pem"): false,
	} {
		s := DefaultSettings()
		s.OIDCAPIKeys.CAFile = file
		_, err := New(Deps{Settings: s})
		if ok != (err == nil) ||
			(err != nil && (!errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), "ca_file"))) {
			t.Errorf("%s: %v", filepath.Base(file), err)
		}
	}
}

func mustNew(t *testing.T, d Deps) *Domain {
	t.Helper()
	dom, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	return dom
}

func TestUnitSettings(t *testing.T) {
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(*Settings)
		want string
	}{
		"ttl":  {func(s *Settings) { s.Tokens.API.TTL = -time.Second }, "auth.tokens.api.ttl"},
		"idle": {func(s *Settings) { s.Session.IdleTimeout = 0 }, "auth.session.idle_timeout"},
		"access": {
			func(s *Settings) { s.Tokens.OAuthAccess.TTL = 0 }, "auth.tokens.oauth_access.ttl",
		},
		"refresh": {
			func(s *Settings) { s.Tokens.OAuthRefresh.TTL = -time.Hour },
			"auth.tokens.oauth_refresh.ttl",
		},
		"code":        {func(s *Settings) { s.Tokens.OAuthCode.TTL = 0 }, "auth.tokens.oauth_code.ttl"},
		"worker":      {func(s *Settings) { s.Tokens.Worker.TTL = 0 }, "auth.tokens.worker.ttl"},
		"run":         {func(s *Settings) { s.Tokens.Run.TTL = -time.Hour }, "auth.tokens.run.ttl"},
		"run api":     {func(s *Settings) { s.Tokens.RunAPI.TTL = 0 }, "auth.tokens.run_api.ttl"},
		"run issuer":  {func(s *Settings) { s.RunAPIToken.Issuer = "/iss" }, "auth.run_api_token.issuer"},
		"max age":     {func(s *Settings) { s.Session.MaxAge = -time.Hour }, "auth.session.max_age"},
		"backend":     {func(s *Settings) { s.Signing.Backend = "hsm" }, "auth.signing.backend"},
		"no kms keys": {func(s *Settings) { s.Signing.Backend = BackendKMS }, "auth.signing.kms_keys is required"},
		"no federation kms keys": {func(s *Settings) {
			s.Signing.Backend = BackendKMS
			s.Signing.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/k/1"}}
		}, "auth.federation.kms_keys is required by auth.signing.backend kms"},
		"local federation keys": {
			func(s *Settings) { s.Federation.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/f/1"}} },
			"auth.federation.kms_keys is set, but auth.signing.backend is local",
		},
		"federation provider": {func(s *Settings) {
			s.Signing.Backend = BackendKMS
			s.Signing.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/k/1"}}
			s.Federation.KMSKeys = []KMSKey{{Provider: "pkcs11", Ref: "k"}}
		}, "auth.federation.kms_keys[0].provider"},
		"federation key shared": {func(s *Settings) {
			s.Signing.Backend = BackendKMS
			s.Signing.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/k/1"}}
			s.Federation.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/k/1"}}
		}, "auth.federation.kms_keys[0] vault:transit/k/1 is also in auth.signing.kms_keys"},
		"federation algorithm": {func(s *Settings) { s.Federation.Algorithm = "HS256" }, "auth.federation.algorithm"},
		"federation issuer": {
			func(s *Settings) { s.Federation.Issuer = "https://iw.example.com/#f" }, "auth.federation.issuer",
		},
		"federation issuer pattern": {
			func(s *Settings) { s.Federation.Issuer = "https://iw.example.com/{org}" }, "has a path of other than segments",
		},
		"federation issuer escaped": {
			func(s *Settings) { s.Federation.Issuer = "https://iw.example.com/a%2Fb" }, "has a path of other than segments",
		},
		"federation issuer empty segment": {
			func(s *Settings) { s.Federation.Issuer = "https://iw.example.com//f" }, "has a path of other than segments",
		},
		"federation issuer dot segment": {
			func(s *Settings) { s.Federation.Issuer = "https://iw.example.com/f/../g" }, "has a path of other than segments",
		},
		"federation jwks_uri": {
			func(s *Settings) { s.Federation.JWKSURI = "keys.example.com/jwks" }, "auth.federation.jwks_uri",
		},
		"federation ttl": {func(s *Settings) { s.Tokens.Federation.TTL = 0 }, "auth.tokens.federation.ttl"},
		"local keys": {
			func(s *Settings) { s.Signing.KMSKeys = []KMSKey{{Provider: "vault", Ref: "transit/k/1"}} },
			"auth.signing.kms_keys is set",
		},
		"kms manual": {func(s *Settings) {
			s.Signing = SigningSettings{
				Backend: BackendKMS, Rotation: RotationManual,
				KMSKeys: []KMSKey{{Provider: "vault", Ref: "transit/k/1"}},
			}
		}, "auth.signing.rotation manual"},
		"kms provider": {func(s *Settings) {
			s.Signing.Backend, s.Signing.KMSKeys = BackendKMS, []KMSKey{{Provider: "local", Ref: "k"}}
		}, "auth.signing.kms_keys[0].provider"},
		"kms ref": {func(s *Settings) {
			s.Signing.Backend, s.Signing.KMSKeys = BackendKMS, []KMSKey{{Provider: "awskms"}}
		}, "auth.signing.kms_keys[0].ref"},
		"kms repeated": {func(s *Settings) {
			s.Signing.Backend = BackendKMS
			s.Signing.KMSKeys = []KMSKey{{Provider: "gcpkms", Ref: "k"}, {Provider: "gcpkms", Ref: "k"}}
		}, "auth.signing.kms_keys[1] repeats gcpkms:k"},
		"rotation":    {func(s *Settings) { s.Signing.Rotation = "" }, "auth.signing.rotation"},
		"relative":    {func(s *Settings) { s.APIIssuer = "/issuer" }, "auth.api_issuer"},
		"credentials": {func(s *Settings) { s.APIIssuer = "https://u:p@iw.example.com" }, "auth.api_issuer"},
		"query":       {func(s *Settings) { s.APIIssuer = "https://iw.example.com?x=1" }, "auth.api_issuer"},
		"http issuer": {func(s *Settings) {
			s.OIDCAPIKeys.AllowedIssuers = []string{"https://ok.example.com", "http://idp.example.com"}
		}, "auth.oidc_api_keys.allowed_issuers[1]"},
		"relative issuer": {func(s *Settings) {
			s.OIDCAPIKeys.AllowedIssuers = []string{"idp.example.com"}
		}, "auth.oidc_api_keys.allowed_issuers[0]"},
		"operator id": {func(s *Settings) {
			s.Operator.Keys = []OperatorKey{{ID: "ops", SecretSHA256: secretSHA256}}
		}, "auth.operator.keys[0].id"},
		"operator repeated": {func(s *Settings) {
			id := kernel.NewULID().String()
			s.Operator.Keys = []OperatorKey{{id, secretSHA256}, {id, secretSHA256}}
		}, "auth.operator.keys[1] repeats"},
		"operator hash": {func(s *Settings) {
			s.Operator.Keys = []OperatorKey{{kernel.NewULID().String(), strings.ToUpper(secretSHA256)}}
		}, "auth.operator.keys[0].secret_sha256"},
		"operator short hash": {func(s *Settings) {
			s.Operator.Keys = []OperatorKey{{kernel.NewULID().String(), "abcd"}}
		}, "auth.operator.keys[0].secret_sha256"},
	} {
		s := DefaultSettings()
		tc.edit(&s)
		err := s.Validate()
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Validate = %v, want ErrConfig naming %s", name, err, tc.want)
		}
	}
	s := DefaultSettings()
	s.APIIssuer = "https://iw.example.com/issuer"
	s.OIDCAPIKeys.AllowedIssuers = []string{"https://token.actions.githubusercontent.com"}
	if err := s.Validate(); err != nil {
		t.Errorf("an issuer with a path and an allowed OIDC issuer: %v", err)
	}
	s = DefaultSettings()
	s.Signing.Backend = BackendKMS
	s.Signing.KMSKeys = []KMSKey{
		{Provider: "awskms", Ref: "k1"},
		{Provider: "vault", Ref: "transit/k/2"},
	}
	s.Federation = FederationSettings{
		Issuer:    "https://iw.example.com/federation",
		JWKSURI:   "http://keys.example.com/jwks",
		Algorithm: "PS256",
		KMSKeys:   []KMSKey{{Provider: "awskms", Ref: "rsa"}},
	}
	if err := s.Validate(); err != nil {
		t.Errorf("two KMS keys and a federation key: %v", err)
	}
	s.Signing.Backend = "hsm"
	if err := s.Validate(); err == nil || strings.Contains(err.Error(), "auth.federation.kms_keys") {
		t.Errorf("an unknown backend: %v, want it named alone", err)
	}
	s = DefaultSettings()
	id := kernel.NewULID()
	s.Operator.Keys = []OperatorKey{{id.String(), secretSHA256}}
	if err := s.Validate(); err != nil {
		t.Errorf("an operator key: %v", err)
	}
	if got := s.Operator.operators()[id]; got != sha256.Sum256([]byte("s3cret")) {
		t.Errorf("operators()[%s] = %x", id, got)
	}
}

// secretSHA256 is the SHA-256 of "s3cret" in hex.
var secretSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte("s3cret")))

func TestUnitPassword(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") || VerifyPassword(h, "wrong horse") ||
		VerifyPassword("$argon2id$x", "correct horse") {
		t.Errorf("VerifyPassword(%s) disagrees with HashPassword", h)
	}
}

func TestUnitTokensLongest(t *testing.T) {
	ts := DefaultSettings().Tokens
	if got := ts.longest(); got != defaultRunTTL {
		t.Errorf("default longest = %s, want the run token's %s", got, defaultRunTTL)
	}
	ts.API.TTL = 48 * time.Hour
	if got := ts.longest(); got != ts.API.TTL {
		t.Errorf("longest = %s, want %s", got, ts.API.TTL)
	}
}
