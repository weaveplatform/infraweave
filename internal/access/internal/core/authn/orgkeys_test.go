package authn_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/authn"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/token"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// orgKey adds an organization key to w's stores and returns its ID and
// secret.
func orgKey(w *world, f func(*apikey.OrganizationKey)) (kernel.ULID, string) {
	secret := apikey.NewOrganizationSecret()
	h, _ := apikey.HashOrganization(secret)
	k := apikey.OrganizationKey{
		OrganizationID: kernel.NewOrganizationID(), ID: kernel.NewULID(), Name: "admin bot",
		SecretHash: h,
	}
	if f != nil {
		f(&k)
	}
	w.f.orgKeys[k.ID] = k
	return k.ID, secret
}

// TestUnitOrganizationKeyExchange checks the organization token: a session
// of the organization in no account, which only the Organization tag's
// routes accept, and whose use takes no seat.
//
// ref: decision 0006 § "API keys and organization keys"
func TestUnitOrganizationKeyExchange(t *testing.T) {
	w := newWorld(t)
	expires := t0.Add(2*time.Hour + 500*time.Millisecond)
	id, secret := orgKey(w, func(k *apikey.OrganizationKey) { k.ExpiresAt = expires })
	g, err := w.s.Exchange(t.Context(), id.String(), secret, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	ss := g.Session
	if ss.ActorKind != kernel.PrincipalOrganization || ss.ActorID != id ||
		!ss.AccountID.IsZero() || ss.OrganizationID != w.f.orgKeys[id].OrganizationID ||
		ss.Channel != session.ChannelUnknown || ss.Name != "admin bot" {
		t.Errorf("session = %+v", ss)
	}
	if g.Claims.Subject != authn.OrganizationKeySubject+id.String() ||
		g.Claims.Kind != token.KindOrganizationKey || !g.Claims.Expiry.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("claims = %+v", g.Claims)
	}
	if w.f.orgTouched != 1 || w.f.touched != 0 {
		t.Errorf("touched organization keys %d, API keys %d", w.f.orgTouched, w.f.touched)
	}
	for reach, ok := range map[authn.Reach]bool{
		authn.ReachAccount:           false,
		authn.ReachOrganization:      false,
		authn.ReachOrganizationAdmin: true,
	} {
		p, _, err := w.s.Authenticate(t.Context(), g.Token, reach)
		if ok && (err != nil || p.Kind != kernel.PrincipalOrganization) {
			t.Errorf("reach %d = %+v, %v", reach, p, err)
		}
		if !ok && !errors.Is(err, authn.ErrUnauthenticated) {
			t.Errorf("reach %d = %v, want unauthenticated", reach, err)
		}
	}
}

func TestUnitOrganizationKeyRefused(t *testing.T) {
	w := newWorld(t)
	id, secret := orgKey(w, nil)
	disabled, dsecret := orgKey(w, func(k *apikey.OrganizationKey) { k.DisabledAt = t0 })
	expired, esecret := orgKey(w, func(k *apikey.OrganizationKey) { k.ExpiresAt = t0 })
	closing, csecret := orgKey(w, func(k *apikey.OrganizationKey) {
		k.ExpiresAt = t0.Add(500 * time.Millisecond)
	})
	for name, c := range map[string][2]string{
		"wrong secret":   {id.String(), apikey.NewOrganizationSecret()},
		"short secret":   {id.String(), apikey.OrganizationPrefix + "abc"},
		"unknown key":    {kernel.NewULID().String(), secret},
		"disabled":       {disabled.String(), dsecret},
		"expired":        {expired.String(), esecret},
		"under a second": {closing.String(), csecret},
		"an API secret":  {id.String(), strings.Replace(secret, "iwo_", "iwk_", 1)},
	} {
		if _, err := w.s.Exchange(
			t.Context(),
			c[0],
			c[1],
			"",
		); !errors.Is(
			err,
			authn.ErrUnauthenticated,
		) {
			t.Errorf("%s = %v", name, err)
		}
	}
	if w.f.orgTouched != 0 {
		t.Errorf("refused exchanges touched %d keys", w.f.orgTouched)
	}

	for _, op := range []string{"OrganizationKey", "TouchOrganizationKey", "Issue", "CreateSession"} {
		w.f.fail = op
		if _, err := w.s.Exchange(t.Context(), id.String(), secret, ""); !errors.Is(err, errDown) {
			t.Errorf("%s down = %v", op, err)
		}
	}
	w.f.fail = ""

	none := authn.New(
		authn.Deps{Keys: w.f, Sessions: w.f, Tokens: w.f, Now: func() time.Time { return t0 }},
	)
	if _, err := none.Exchange(
		t.Context(),
		id.String(),
		secret,
		"",
	); !errors.Is(
		err,
		authn.ErrUnauthenticated,
	) {
		t.Errorf("without organization keys = %v", err)
	}
}

// TestUnitOrganizationAdminReach checks which browser sessions the
// Organization tag's routes admit: a member's, in an account or not, and
// the break-glass admin's, but no other kind's.
func TestUnitOrganizationAdminReach(t *testing.T) {
	w := newWorld(t)
	org := kernel.NewOrganizationID()
	for kind, ok := range map[kernel.PrincipalKind]bool{
		kernel.PrincipalUser:       true,
		kernel.PrincipalBreakGlass: true,
		kernel.PrincipalAPIKey:     false,
		kernel.PrincipalRun:        false,
	} {
		for _, acct := range []kernel.AccountID{{}, kernel.NewAccountID()} {
			secret := kernel.NewULID().String()
			sid := kernel.NewULID()
			w.f.sessions[sid] = session.Session{
				ID: sid, OrganizationID: org, AccountID: acct, ActorKind: kind,
				ActorID: kernel.NewULID(), Channel: session.ChannelWeb,
				SecretHash: session.HashSecret(secret), ExpiresAt: t0.Add(time.Hour),
				IdleExpiresAt: t0.Add(time.Hour),
			}
			_, _, err := w.s.AuthenticateBrowser(t.Context(), secret, authn.ReachOrganizationAdmin)
			if ok != (err == nil) {
				t.Errorf("%s in account %v = %v, want admitted %t", kind, acct, err, ok)
			}
		}
	}
}
