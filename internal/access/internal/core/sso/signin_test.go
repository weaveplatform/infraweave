package sso_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitParsePurpose(t *testing.T) {
	for in, want := range map[string]sso.Purpose{
		"": sso.PurposeSignIn, "sign-in": sso.PurposeSignIn, "enable": sso.PurposeEnable,
		"link": sso.PurposeLink, "reauthenticate": sso.PurposeReauthenticate,
	} {
		if got, err := sso.ParsePurpose(in); err != nil || got != want {
			t.Errorf("ParsePurpose(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := sso.ParsePurpose("SIGN_IN"); !errors.Is(err, sso.ErrNotOffered) {
		t.Errorf("an unknown purpose: %v", err)
	}
	for p, fresh := range map[sso.Purpose]bool{
		sso.PurposeSignIn: false, sso.PurposeEnable: false,
		sso.PurposeLink: true, sso.PurposeReauthenticate: true,
	} {
		if p.Fresh() != fresh {
			t.Errorf("%s.Fresh() = %t", p, !fresh)
		}
	}
}

// claims are a good ID token's claims for req, which the cases break.
func claims(req sso.Request) map[string]any {
	return map[string]any{
		"iss": "https://idp.test", "sub": "alice-sub", "aud": "client", "nonce": req.Nonce,
		"auth_time": float64(t0.Unix()),
	}
}

func TestUnitCheckIDToken(t *testing.T) {
	signIn := sso.Request{Nonce: "n-1", Purpose: sso.PurposeSignIn}
	fresh := sso.Request{Nonce: "n-1", Purpose: sso.PurposeReauthenticate}
	for name, c := range map[string]struct {
		req    sso.Request
		change func(map[string]any)
		ok     bool
	}{
		"good":                     {req: signIn, change: func(map[string]any) {}, ok: true},
		"no auth_time, sign-in":    {req: signIn, change: func(c map[string]any) { delete(c, "auth_time") }, ok: true},
		"good, fresh":              {req: fresh, change: func(map[string]any) {}, ok: true},
		"auth_time within skew":    {req: fresh, change: func(c map[string]any) { c["auth_time"] = float64(t0.Add(-sso.Skew).Unix()) }, ok: true},
		"one audience in an array": {req: signIn, change: func(c map[string]any) { c["aud"] = []any{"client"} }, ok: true},
		"two audiences with azp": {req: signIn, change: func(c map[string]any) {
			c["aud"], c["azp"] = []any{"client", "other", 7}, "client"
		}, ok: true},
		"wrong nonce":          {req: signIn, change: func(c map[string]any) { c["nonce"] = "n-2" }},
		"no nonce":             {req: signIn, change: func(c map[string]any) { delete(c, "nonce") }},
		"request has no nonce": {req: sso.Request{}, change: func(c map[string]any) { c["nonce"] = "" }},
		"empty sub":            {req: signIn, change: func(c map[string]any) { c["sub"] = "" }},
		"sub not a string":     {req: signIn, change: func(c map[string]any) { c["sub"] = 7.0 }},
		"two audiences, no azp": {req: signIn, change: func(c map[string]any) {
			c["aud"] = []any{"client", "other"}
		}},
		"two audiences, another azp": {req: signIn, change: func(c map[string]any) {
			c["aud"], c["azp"] = []any{"client", "other"}, "other"
		}},
		"no auth_time, fresh": {req: fresh, change: func(c map[string]any) { delete(c, "auth_time") }},
		"auth_time a string":  {req: fresh, change: func(c map[string]any) { c["auth_time"] = "now" }},
		"auth_time past skew": {req: fresh, change: func(c map[string]any) { c["auth_time"] = float64(t0.Add(-sso.Skew - time.Second).Unix()) }},
	} {
		t.Run(name, func(t *testing.T) {
			cl := claims(c.req)
			c.change(cl)
			err := sso.CheckIDToken(cl, c.req, "client", t0)
			if c.ok != (err == nil) || err != nil && !errors.Is(err, sso.ErrToken) {
				t.Errorf("CheckIDToken = %v, want ok %t", err, c.ok)
			}
		})
	}
}

func TestUnitIdentity(t *testing.T) {
	var none sso.Settings
	entra := sso.Settings{ClaimMapping: []sso.ClaimMappingEntry{
		{Name: "subject", Value: "oid"}, {Name: "login", Value: "upn"}, {Name: "groups", Value: "roles"},
	}}
	for name, c := range map[string]struct {
		s      sso.Settings
		claims map[string]any
		want   sso.Identity
		err    bool
	}{
		"defaults": {s: none, claims: map[string]any{
			"sub": "s-1", "preferred_username": "alice", "name": "Alice", "email": "a@x.test",
			"email_verified": true, "groups": []any{"dev", "ops", "dev", 3}, "sid": "sid-1",
		}, want: sso.Identity{
			Subject: "s-1", Login: "alice", Name: "Alice", Email: "a@x.test",
			Groups: []string{"dev", "ops"}, SID: "sid-1",
		}},
		"login from email, unverified": {s: none, claims: map[string]any{
			"sub": "s-1", "email": "a@x.test", "email_verified": false, "groups": "dev",
		}, want: sso.Identity{Subject: "s-1", Login: "a@x.test", Name: "a@x.test", Groups: []string{"dev"}}},
		"login from sub, verified as a string": {s: none, claims: map[string]any{
			"sub": "s-1", "email_verified": "true", "preferred_username": "",
		}, want: sso.Identity{Subject: "s-1", Login: "s-1", Name: "s-1", Groups: []string{}}},
		"groups elsewhere": {s: none, claims: map[string]any{
			"sub": "s-1", "groups": []any{"dev"}, "_claim_names": map[string]any{"groups": "src1"},
		}, want: sso.Identity{Subject: "s-1", Login: "s-1", Name: "s-1", Groups: []string{}}},
		"groups of another type": {
			s: none, claims: map[string]any{"sub": "s-1", "groups": 7.0},
			want: sso.Identity{Subject: "s-1", Login: "s-1", Name: "s-1", Groups: []string{}},
		},
		"mapped subject": {s: entra, claims: map[string]any{
			"sub": "pairwise", "oid": "object-1", "upn": "alice@x.test", "roles": []any{"admins"},
		}, want: sso.Identity{
			Subject: "object-1", Login: "alice@x.test", Name: "alice@x.test", Groups: []string{"admins"},
		}},
		"mapped login missing": {
			s: entra, claims: map[string]any{"sub": "pairwise", "oid": "object-1"},
			want: sso.Identity{Subject: "object-1", Login: "object-1", Name: "object-1", Groups: []string{}},
		},
		"mapped subject missing": {s: entra, claims: map[string]any{"sub": "pairwise"}, err: true},
		"no sub":                 {s: none, claims: map[string]any{"preferred_username": "alice"}, err: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := c.s.Identity(c.claims)
			if c.err {
				if !errors.Is(err, sso.ErrToken) {
					t.Errorf("Identity = %+v, %v; want ErrToken", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(c.want, got); d != "" {
				t.Errorf("Identity (-want +got):\n%s", d)
			}
		})
	}
}

func TestUnitCheckReturnTo(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/": "/", "/stacks?x=1#top": "/stacks?x=1#top", "/a/b": "/a/b",
	} {
		if got, err := sso.CheckReturnTo(in); err != nil || got != want {
			t.Errorf("CheckReturnTo(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"https://evil.test/", "//evil.test/x", "/\\evil.test", "stacks", "javascript:alert(1)",
		"/a\nb", "%zz", "http:/x",
	} {
		if got, err := sso.CheckReturnTo(in); !errors.Is(err, sso.ErrNotOffered) {
			t.Errorf("CheckReturnTo(%q) = %q, %v", in, got, err)
		}
	}
}

func TestUnitResolveMember(t *testing.T) {
	a, b := kernel.NewMemberID(), kernel.NewMemberID()
	for name, c := range map[string]struct {
		bound   *sso.Candidate
		pending []sso.Candidate
		res     sso.Resolution
		id      kernel.MemberID
		err     error
	}{
		"bound active":       {bound: &sso.Candidate{ID: a, Status: member.Active}, res: sso.ResolveExisting, id: a},
		"bound pending":      {bound: &sso.Candidate{ID: a, Status: member.Pending}, res: sso.ResolveActivate, id: a},
		"bound deactivated":  {bound: &sso.Candidate{ID: a, Status: member.Deactivated}, err: sso.ErrDeactivated},
		"bound wins pending": {bound: &sso.Candidate{ID: a, Status: member.Active}, pending: []sso.Candidate{{ID: b}}, res: sso.ResolveExisting, id: a},
		"unknown":            {res: sso.ResolveCreate},
		"one pending":        {pending: []sso.Candidate{{ID: b, Status: member.Pending}}, res: sso.ResolveActivate, id: b},
		"two pending":        {pending: []sso.Candidate{{ID: a}, {ID: b}}, err: sso.ErrNotOffered},
	} {
		t.Run(name, func(t *testing.T) {
			res, id, err := sso.ResolveMember(c.bound, c.pending)
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Errorf("ResolveMember = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil || res != c.res || id != c.id {
				t.Errorf("ResolveMember = %v, %v, %v; want %v, %v", res, id, err, c.res, c.id)
			}
		})
	}
}

func TestUnitScopes(t *testing.T) {
	if !slices.Equal(sso.Scopes, []string{"openid", "email", "profile", "groups"}) ||
		sso.RequestTTL != 10*time.Minute || sso.Skew != time.Minute ||
		sso.RequestCookie != "__Host-infraweave_sso" || sso.CallbackPath != "/auth/oidc/callback" {
		t.Errorf("the sign-in's constants changed: %v %v %v %q %q",
			sso.Scopes, sso.RequestTTL, sso.Skew, sso.RequestCookie, sso.CallbackPath)
	}
}
