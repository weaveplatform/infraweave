package pg_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/integration"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// apps is a GitHub host that knows the code "good", reports every App
// OK, and writes a manifest naming its webhook and redirect URLs.
type apps struct{}

func (apps) App(_ context.Context, in port.Integration) port.App {
	return port.App{
		Status: port.AppOK, URL: "https://github.example/apps/" + in.Settings.AppID,
		Installations: []port.Installation{{ID: 7, Account: "acme"}},
	}
}

func (apps) Manifest(host, owner, webhookURL, redirectURL, state string) (port.Manifest, error) {
	return port.Manifest{
		Endpoint: host + "/organizations/" + owner + "/settings/apps/new?state=" + state,
		Manifest: webhookURL + " " + redirectURL,
	}, nil
}

func (apps) Convert(_ context.Context, apiHost, code string) (port.Created, error) {
	if code != "good" {
		return port.Created{}, port.ErrNotFound
	}
	return port.Created{
		Name: "Acme App " + strings.TrimPrefix(apiHost, "https://"), AppID: "42",
		PrivateKey: appKey(), WebhookSecret: []byte("app-secret"),
	}, nil
}

// appKey is the private key of every App apps converts a code into.
var appKey = sync.OnceValue(func() []byte {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k),
	})
})

// TestAccBuiltin checks that the operator's App answers its defaults until
// an account stores its check options, keeps a nil option, refuses an
// unknown mode, and is what a GITHUB stack uses; and that a service with
// no operator App answers integration.ErrNotConfigured.
func TestAccBuiltin(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	in, err := f.svc.Builtin(ctx, f.admin, "GITHUB")
	if err != nil || in.Checks.Mode != "INDIVIDUAL" || in.Settings.AppID != "1" || !in.IsDefault {
		t.Fatalf("Builtin before a write = %+v, %v", in, err)
	}
	yes := true
	in, err = f.svc.SetBuiltin(ctx, f.admin, "GITHUB", integration.Checks{Mode: "ALL"}, &yes)
	if err != nil || in.Checks.Mode != "ALL" || !in.UseGitCheckout || in.Version != 1 {
		t.Fatalf("SetBuiltin = %+v, %v", in, err)
	}
	in, err = f.svc.SetBuiltin(ctx, f.admin, "GITHUB", integration.Checks{
		Mode: "AGGREGATED", IndividualFailuresOnly: true,
	}, nil)
	if err != nil || in.Checks.Mode != "AGGREGATED" || !in.UseGitCheckout || in.Version != 2 ||
		!in.Checks.IndividualFailuresOnly {
		t.Fatalf("second SetBuiltin = %+v, %v", in, err)
	}
	if _, err := f.svc.SetBuiltin(
		ctx,
		f.admin,
		"GITHUB",
		integration.Checks{Mode: "SOME"},
		nil,
	); !slices.Equal(
		pointers(t, err),
		[]string{"/vcsChecks"},
	) {
		t.Errorf("an unknown mode = %v", err)
	}
	use, ok, err := f.svc.Use(ctx, f.acct, "GITHUB", &in.ID)
	if err != nil || !ok || string(use.Credential) != "operator-key" {
		t.Errorf("Use(GITHUB) = %+v, %v, %v", use, ok, err)
	}

	bare := f.deps
	bare.Builtin, bare.Apps = nil, nil
	svc := integration.New(bare)
	for name, err := range map[string]error{
		"Builtin": func() error { _, err := svc.Builtin(ctx, f.admin, "GITHUB"); return err }(),
		"SetBuiltin": func() error {
			_, err := svc.SetBuiltin(ctx, f.admin, "GITHUB", integration.Checks{Mode: "ALL"}, nil)
			return err
		}(),
		"OpenManifest": func() error {
			_, err := svc.OpenManifest(ctx, f.admin, "", "", "acme")
			return err
		}(),
		"FromManifest": func() error {
			_, err := svc.FromManifest(ctx, f.admin, "good", "state")
			return err
		}(),
	} {
		if !errors.Is(err, integration.ErrNotConfigured) {
			t.Errorf("%s with no operator App = %v", name, err)
		}
	}
}

// TestAccManifest checks that a manifest form names its webhook and
// redirect URLs, that completing it creates the default GitHub Enterprise
// integration with the form's ID, that the next is not the default, and
// that a completed, unknown, expired or malformed state, an unknown code
// and a host that is not https are refused at their fields.
func TestAccManifest(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	m, err := f.svc.OpenManifest(ctx, f.admin, "", "", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state := m.Endpoint[strings.LastIndex(m.Endpoint, "=")+1:]
	if !strings.HasPrefix(m.Endpoint, "https://github.com/organizations/acme/") ||
		m.Manifest != "https://infraweave.example/webhooks/vcs/github_enterprise/"+state+
			" https://infraweave.example/vcs/github-enterprise/manifest" {
		t.Errorf("manifest = %+v", m)
	}
	in, err := f.svc.FromManifest(ctx, f.admin, "good", state)
	if err != nil || in.ID.String() != state || !in.IsDefault ||
		in.Name != "Acme App api.github.com" || in.Settings.AppID != "42" ||
		in.Settings.UserFacingHost != "https://github.com" {
		t.Fatalf("FromManifest = %+v, %v", in, err)
	}
	d, err := f.svc.Details(ctx, in)
	if err != nil || d.WebhookSecret != "app-secret" || d.App == nil ||
		d.App.URL != "https://github.example/apps/42" ||
		d.WebhookURL != "https://infraweave.example/webhooks/vcs/github_enterprise/"+state {
		t.Errorf("Details = %+v, %v", d, err)
	}
	if _, err := f.svc.FromManifest(ctx, f.admin, "good", state); !slices.Equal(
		pointers(t, err), []string{"/state"}) {
		t.Errorf("a completed form = %v", err)
	}
	m, err = f.svc.OpenManifest(
		ctx,
		f.admin,
		"https://ghe.example/",
		"https://ghe.example/api/v3",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	next := m.Endpoint[strings.LastIndex(m.Endpoint, "=")+1:]
	if _, err := f.svc.FromManifest(ctx, f.admin, "bad", next); !slices.Equal(
		pointers(t, err), []string{"/code"}) {
		t.Errorf("an unknown code = %v", err)
	}
	m, _ = f.svc.OpenManifest(ctx, f.admin, "https://ghe.example", "https://ghe.example/api/v3", "")
	next = m.Endpoint[strings.LastIndex(m.Endpoint, "=")+1:]
	in, err = f.svc.FromManifest(ctx, f.admin, "good", next)
	if err != nil || in.IsDefault || in.Settings.UserFacingHost != "https://ghe.example" {
		t.Errorf("a second App = %+v, %v", in, err)
	}

	m, _ = f.svc.OpenManifest(ctx, f.admin, "", "", "")
	late := f.deps
	late.Now = func() time.Time { return time.Unix(1_700_000_000, 0).Add(integration.ManifestTTL) }
	expired := m.Endpoint[strings.LastIndex(m.Endpoint, "=")+1:]
	for _, state := range []string{expired, kernel.NewULID().String(), "nope"} {
		if _, err := integration.New(late).FromManifest(ctx, f.admin, "good", state); !slices.Equal(
			pointers(t, err), []string{"/state"}) {
			t.Errorf("state %s = %v", state, err)
		}
	}
	_, err = f.svc.OpenManifest(ctx, f.admin, "http://github.example", "https://user:pw@x", "")
	if got := pointers(
		t,
		err,
	); !slices.Contains(got, "/host") ||
		!slices.Contains(got, "/apiHost") {
		t.Errorf("hosts that are not https = %v", got)
	}
}

// TestAccAppSettings checks that a create refuses a host that is not
// https, an App ID that is not a number and a private key that is not
// one; that an update keeps the private key when it carries none and
// refuses another space; and that an integration with no App reports
// none.
func TestAccAppSettings(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	key := appKey()
	in := integration.Input{
		Provider: "GITHUB_ENTERPRISE", Name: "Enterprise", Labels: []string{},
		Settings: port.Settings{
			APIHost: "https://ghe.example", UserFacingHost: "https://ghe.example", AppID: "7",
		},
		Secrets: &integration.Secrets{Credential: key, WebhookSecret: []byte("s")},
	}
	bad := in
	bad.Settings = port.Settings{APIHost: "ftp://ghe", UserFacingHost: "https://", AppID: "x7"}
	bad.Secrets = &integration.Secrets{Credential: []byte("not a key")}
	got := pointers(t, func() error { _, err := f.svc.Create(ctx, f.admin, bad); return err }())
	slices.Sort(got)
	if !slices.Equal(got, []string{"/apiHost", "/appID", "/privateKey", "/userFacingHost"}) {
		t.Errorf("bad settings = %v", got)
	}
	bad.Settings.AppID, bad.Settings.APIHost, bad.Settings.UserFacingHost = "7", "", ""
	if got := pointers(
		t,
		func() error { _, err := f.svc.Create(ctx, f.admin, bad); return err }(),
	); !slices.Equal(
		got,
		[]string{"/privateKey"},
	) {
		t.Errorf("a bad key = %v", got)
	}
	pkcs8 := in
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8.Name = "PKCS8"
	pkcs8.Secrets = &integration.Secrets{
		Credential: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
	}
	if _, err := f.svc.Create(ctx, f.admin, pkcs8); err != nil {
		t.Errorf("a PKCS #8 key = %v", err)
	}

	out, err := f.svc.Create(ctx, f.admin, in)
	if err != nil {
		t.Fatal(err)
	}
	up := in
	up.Space, up.Description = "acme", "renewed"
	up.Secrets = &integration.Secrets{WebhookSecret: []byte("new")}
	if _, err := f.svc.Update(ctx, f.admin, out.ID.String(), kernel.Expect{}, up); err != nil {
		t.Fatal(err)
	}
	sec, err := f.store.Secrets(ctx, f.acct, out.ID)
	if err != nil || string(sec.Credential) != string(key) || string(sec.WebhookSecret) != "new" {
		t.Errorf("secrets after an update with no key = %q, %q, %v",
			sec.Credential, sec.WebhookSecret, err)
	}
	for _, space := range []string{"team", "nowhere"} {
		up.Space = space
		_, err := f.svc.Update(ctx, f.admin, out.ID.String(), kernel.Expect{}, up)
		if !slices.Equal(pointers(t, err), []string{"/customInput/space"}) {
			t.Errorf("an update to space %s = %v", space, err)
		}
	}

	gl, err := f.svc.Create(ctx, f.admin, gitlab("Lab", "", false))
	if err != nil {
		t.Fatal(err)
	}
	if d, err := f.svc.Details(ctx, gl); err != nil || d.App != nil ||
		d.WebhookURL != "https://infraweave.example/webhooks/vcs/gitlab/"+gl.ID.String() {
		t.Errorf("Details of an integration with no App = %+v, %v", d, err)
	}
}
