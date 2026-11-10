package github_test

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/githubfake"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// TestUnitTest checks the checks of an App that has what infraweave
// needs, one that lacks some, and one that cannot be read.
func TestUnitTest(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	passed := func(checks []port.Check) []bool {
		out := make([]bool, 0, len(checks))
		for _, c := range checks {
			out = append(out, c.Passed)
		}
		return out
	}
	got, err := l.gh.Test(ctx, l.in)
	if err != nil || !slices.Equal(passed(got), []bool{true, true, true}) {
		t.Errorf("a complete App = %+v, %v", got, err)
	}

	l.fake.SetApp(l.app.ID, map[string]string{
		"checks": "read", "contents": "write", "metadata": "read", "statuses": "write",
	}, []string{"push"})
	got, _ = l.gh.Test(ctx, l.in)
	if !slices.Equal(passed(got), []bool{true, false, false}) ||
		got[1].Reason != "The App lacks the permissions checks:write, pull_requests:write." ||
		got[2].Reason != "The App does not send the events pull_request." {
		t.Errorf("a lacking App = %+v", got)
	}

	for name, in := range map[string]port.Integration{
		"bad key": {Settings: port.Settings{APIHost: l.srv.URL, AppID: "1"}},
		"unknown": integrationOf(githubfake.App{ID: 1, PrivateKey: l.app.PrivateKey}, l.srv.URL),
	} {
		got, err := l.gh.Test(ctx, in)
		if err != nil || !slices.Equal(passed(got), []bool{false, false, false}) ||
			got[0].Reason == "" {
			t.Errorf("%s: %+v, %v", name, got, err)
		}
	}
}

// TestUnitApp checks what the provider reports of an App, one whose key
// does not decode, one its host does not know, and a failing host.
func TestUnitApp(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	got := l.gh.App(ctx, l.in)
	if got.Status != port.AppOK || got.URL != l.srv.URL+"/apps/infraweave" ||
		len(got.Installations) != 1 || got.Installations[0].Account != "acme" ||
		got.Installations[0].AccountType != "Organization" || got.Installations[0].URL == "" {
		t.Errorf("App = %+v", got)
	}
	if s := l.gh.App(ctx, port.Integration{}).Status; s != port.AppCouldNotDecode {
		t.Errorf("no key = %s", s)
	}
	unknown := integrationOf(githubfake.App{ID: 1, PrivateKey: l.app.PrivateKey}, l.srv.URL)
	if s := l.gh.App(ctx, unknown).Status; s != port.AppNotFound {
		t.Errorf("unknown App = %s", s)
	}
	l.fake.Fail(githubfake.ServerError, 0)
	if s := l.gh.App(ctx, l.in).Status; s != port.AppError {
		t.Errorf("failing host = %s", s)
	}
}

// TestUnitManifest checks the manifest form for an organization and for
// a user, and refuses a host or redirect that is not a URL.
func TestUnitManifest(t *testing.T) {
	gh := github.New(github.Options{})
	m, err := gh.Manifest(
		"",
		"acme",
		"https://iw.example/webhooks/github",
		"https://iw.example/back",
		"s t",
	)
	if err != nil {
		t.Fatal(err)
	}
	if m.Endpoint != "https://github.com/organizations/acme/settings/apps/new?state=s+t" {
		t.Errorf("endpoint = %s", m.Endpoint)
	}
	var got struct {
		URL            string `json:"url"`
		Public         bool   `json:"public"`
		HookAttributes struct {
			URL string `json:"url"`
		} `json:"hook_attributes"` //nolint:tagliatelle // GitHub's format
		DefaultPermissions map[string]string `json:"default_permissions"` //nolint:tagliatelle // as above
		DefaultEvents      []string          `json:"default_events"`      //nolint:tagliatelle // as above
	}
	if err := json.Unmarshal([]byte(m.Manifest), &got); err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://iw.example" || got.Public ||
		got.HookAttributes.URL != "https://iw.example/webhooks/github" ||
		got.DefaultPermissions["contents"] != "read" ||
		!slices.Equal(got.DefaultEvents, []string{"pull_request", "push"}) {
		t.Errorf("manifest = %+v", got)
	}
	m, err = gh.Manifest("https://ghe.example/", "", "w", "https://iw.example/back", "s")
	if err != nil || m.Endpoint != "https://ghe.example/settings/apps/new?state=s" {
		t.Errorf("user endpoint = %s, %v", m.Endpoint, err)
	}
	for _, c := range [][2]string{{"ghe.example", "https://iw.example"}, {"", "back"}} {
		if _, err := gh.Manifest(c[0], "", "w", c[1], "s"); !errors.Is(err, github.ErrHost) {
			t.Errorf("Manifest(%q, %q) = %v", c[0], c[1], err)
		}
	}
}

// TestUnitConvert checks that a manifest code converts once into the App
// it created.
func TestUnitConvert(t *testing.T) {
	l := newLab(t)
	ctx := t.Context()
	code := l.fake.ManifestCode("acme")
	got, err := l.gh.Convert(ctx, l.srv.URL, code)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(got.AppID, 10, 64)
	app, ok := l.fake.App(id)
	if !ok || got.Name != app.Name || string(got.PrivateKey) != string(app.PrivateKey) ||
		string(got.WebhookSecret) != app.WebhookSecret {
		t.Errorf("Convert = %+v, App %+v", got, app)
	}
	if _, err := l.gh.Convert(ctx, l.srv.URL, code); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("a used code = %v", err)
	}
	if _, err := l.gh.Convert(ctx, "nope", url.PathEscape(code)); !errors.Is(err, github.ErrHost) {
		t.Errorf("a bad host = %v", err)
	}
	if _, ok := l.fake.App(1); ok {
		t.Error("the fake knows App 1")
	}
}
