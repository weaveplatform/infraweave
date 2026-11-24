package settings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/federation"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/orgadmin"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitCheckRedirectHosts(t *testing.T) {
	got, err := CheckRedirectHosts([]string{
		"Example.COM", "*.apps.example.com", "localhost",
		"xn--bcher-kva.example", "a-b.c1.example",
	})
	want := []string{
		"example.com", "*.apps.example.com", "localhost", "xn--bcher-kva.example",
		"a-b.c1.example",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckRedirectHosts = %v, %v; want %v", got, err, want)
	}
	if got, err := CheckRedirectHosts(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no hosts = %#v, %v; want an empty list", got, err)
	}
	for _, bad := range []string{
		"", "https://example.com", "example.com:443", "example.com/cb", "user@example.com",
		"*", "*.", "**.example.com", "*.*.example.com", "a.*.example.com", "example..com",
		".example.com", "example.com.", "-a.example.com", "a-.example.com", "ex ample.com",
		"exämple.com", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "com",
	} {
		_, err := CheckRedirectHosts([]string{"ok.example.com", bad})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "hosts[1]") {
			t.Errorf("%q: %v, want ErrInvalid naming hosts[1]", bad, err)
		}
	}
	if _, err := CheckRedirectHosts([]string{"a.example.com", "A.example.com"}); !errors.Is(err,
		ErrInvalid) || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a host given twice: %v", err)
	}
}

func TestUnitCheckSecurityEmail(t *testing.T) {
	for _, ok := range []string{"", "sec@example.com", "a.b+c@sub.example.org"} {
		if err := CheckSecurityEmail(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"sec", "Sec <sec@example.com>", "sec@", " sec@example.com"} {
		if err := CheckSecurityEmail(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", bad, err)
		}
	}
}

func TestUnitDeletable(t *testing.T) {
	m := &Mark{At: time.Now()}
	for _, c := range []struct {
		s    Settings
		want bool
	}{
		{Settings{}, false},
		{Settings{Mark: m}, true},
		{Settings{Mark: m, LastAccount: true}, false},
		{Settings{LastAccount: true}, false},
	} {
		if got := c.s.Deletable(); got != c.want {
			t.Errorf("%+v: Deletable() = %t", c.s, got)
		}
	}
	if d := Defaults(); !d.PersonalAPIKeys || d.NonHumanKeyManagers || d.RedirectHosts == nil {
		t.Errorf("Defaults() = %+v", d)
	}
}

// mem is Store in memory, failing every call with err when set.
type mem struct {
	s        Settings
	disabled bool
	audit    []kernel.AuditEntry
	org      []kernel.OrganizationAuditEntry
	revoked  []kernel.ULID
	err      error
}

func (m *mem) Settings(context.Context, kernel.AccountID) (Settings, error) { return m.s, m.err }

func (m *mem) SetAuthorizationScheme(_ context.Context, _ kernel.Principal, next accessapi.AuthorizationScheme, _ kernel.AuditEntry) ([]kernel.ULID, error) {
	m.s.AuthorizationScheme = next
	return m.revoked, m.err
}

func (m *mem) SetPersonalAPIKeys(_ context.Context, _ kernel.AccountID, enabled, disable bool) ([]kernel.ULID, error) {
	m.s.PersonalAPIKeys, m.disabled = enabled, disable
	if !disable {
		return nil, m.err
	}
	return m.revoked, m.err
}

func (m *mem) ToggleNonHumanKeyManagers(context.Context, kernel.AccountID) (bool, error) {
	m.s.NonHumanKeyManagers = !m.s.NonHumanKeyManagers
	return m.s.NonHumanKeyManagers, m.err
}

func (m *mem) SetSecurityEmail(_ context.Context, _ kernel.AccountID, e string) error {
	m.s.SecurityEmail = e
	return m.err
}

func (m *mem) SetOIDCSubjectTemplate(_ context.Context, _ kernel.AccountID, t string) error {
	m.s.OIDCSubjectTemplate = t
	return m.err
}

func (m *mem) SetRedirectHosts(_ context.Context, _ kernel.AccountID, h []string) error {
	m.s.RedirectHosts = h
	return m.err
}

func (m *mem) SetSurvey(_ context.Context, _ kernel.AccountID, sv Survey) error {
	m.s.Survey = sv
	return m.err
}

func (m *mem) ToggleDeletionMark(_ context.Context, _ kernel.AccountID, mk Mark) (bool, error) {
	if m.s.Mark == nil {
		m.s.Mark = &mk
	} else {
		m.s.Mark = nil
	}
	return m.s.Mark != nil, m.err
}

func (m *mem) RequestPersonalAPIKeys(_ context.Context, e kernel.AuditEntry) error {
	m.audit = append(m.audit, e)
	return m.err
}

func (m *mem) DeleteAccount(_ context.Context, _ kernel.AccountID, e kernel.OrganizationAuditEntry) ([]kernel.ULID, error) {
	m.org = append(m.org, e)
	return m.revoked, m.err
}

func newService(m *mem, now time.Time) (*Service, *[]kernel.ULID) {
	var dropped []kernel.ULID
	return NewService(Deps{
		Store: m,
		Drop:  func(id kernel.ULID) { dropped = append(dropped, id) },
		Now:   func() time.Time { return now },
	}), &dropped
}

func TestUnitService(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	m := &mem{s: Defaults(), revoked: []kernel.ULID{kernel.NewULID()}}
	svc, dropped := newService(m, now)
	member := kernel.Principal{
		Kind:           kernel.PrincipalUser,
		ActorID:        kernel.NewULID(),
		OrganizationID: kernel.NewOrganizationID(),
		AccountID:      kernel.NewAccountID(),
		Login:          "alice",
	}
	key := member
	key.Kind = kernel.PrincipalAPIKey

	if on, err := svc.SetPersonalAPIKeys(ctx, member, true, true); err != nil || !on || m.disabled {
		t.Errorf("enabling with disableExistingKeys disabled keys: %t %t %v", on, m.disabled, err)
	}
	if on, err := svc.SetPersonalAPIKeys(
		ctx,
		member,
		false,
		true,
	); err != nil || on ||
		!m.disabled ||
		len(*dropped) != 1 {
		t.Errorf(
			"refusing with disableExistingKeys: %t %t %v, dropped %v",
			on,
			m.disabled,
			err,
			*dropped,
		)
	}
	if _, err := svc.ToggleNonHumanKeyManagers(ctx, key); !errors.Is(err, ErrNotHuman) ||
		m.s.NonHumanKeyManagers {
		t.Errorf("a key flipping its own management: %v", err)
	}
	for _, p := range []kernel.Principal{member, {Kind: kernel.PrincipalBreakGlass}} {
		if on, err := svc.ToggleNonHumanKeyManagers(ctx, p); err != nil || !on {
			t.Errorf("%s flipping it = %t, %v", p.Kind, on, err)
		}
		m.s.NonHumanKeyManagers = false
	}
	if _, err := svc.SetSecurityEmail(ctx, member, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a bad address = %v", err)
	}
	if e, err := svc.SetSecurityEmail(ctx, member, "sec@example.com"); err != nil ||
		e != "sec@example.com" || m.s.SecurityEmail != e {
		t.Errorf("SetSecurityEmail = %q, %v", e, err)
	}
	if _, err := svc.SetOIDCSubjectTemplate(ctx, member, "space {spaceId}"); !errors.Is(err, ErrInvalid) ||
		!errors.Is(err, federation.ErrInvalid) {
		t.Errorf("a bad subject template = %v", err)
	}
	if tpl, err := svc.SetOIDCSubjectTemplate(ctx, member, "space:{spaceId}"); err != nil ||
		tpl != "space:{spaceId}" || m.s.OIDCSubjectTemplate != tpl {
		t.Errorf("SetOIDCSubjectTemplate = %q, %v", tpl, err)
	}
	if tpl, err := svc.SetOIDCSubjectTemplate(ctx, member, ""); err != nil ||
		tpl != federation.DefaultSubjectTemplate || m.s.OIDCSubjectTemplate != "" {
		t.Errorf("restoring the default subject template = %q, %v", tpl, err)
	}
	if _, err := svc.SetRedirectHosts(
		ctx,
		member,
		[]string{"https://x"},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Errorf("a bad host = %v", err)
	}
	if h, err := svc.SetRedirectHosts(ctx, member, []string{"X.example.com"}); err != nil ||
		!reflect.DeepEqual(
			h,
			[]string{"x.example.com"},
		) || !reflect.DeepEqual(m.s.RedirectHosts, h) {
		t.Errorf("SetRedirectHosts = %v, %v", h, err)
	}
	r := "a friend"
	if sv, err := svc.SetSurvey(ctx, member, Survey{Completed: true, Response: &r}); err != nil ||
		!sv.Completed || m.s.Survey.Response != &r {
		t.Errorf("SetSurvey = %+v, %v", sv, err)
	}
	reason := "moving"
	if marked, err := svc.ToggleDeletionMark(ctx, member, &reason, nil); err != nil || !marked ||
		!m.s.Mark.At.Equal(now) || m.s.Mark.Reason != &reason {
		t.Errorf("ToggleDeletionMark = %t, %v, %+v", marked, err, m.s.Mark)
	}
	if got, err := svc.Get(ctx, member); err != nil || !got.Deletable() {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if err := svc.RequestPersonalAPIKeys(
		ctx,
		member,
		"192.0.2.7",
	); err != nil || len(m.audit) != 1 ||
		m.audit[0].Action != ActionRequestPersonalAPIKeys || m.audit[0].Actor.Username != "alice" ||
		m.audit[0].AccountID != member.AccountID.ULID ||
		m.audit[0].RemoteIP != "192.0.2.7" {
		t.Errorf("RequestPersonalAPIKeys: %v, %+v", err, m.audit)
	}
	*dropped = nil
	if err := svc.Delete(ctx, member, "192.0.2.7"); err != nil || len(m.org) != 1 ||
		m.org[0].Action != ActionDelete || m.org[0].OrganizationID != member.OrganizationID ||
		m.org[0].AffectedResource.ID != member.AccountID.String() || len(*dropped) != 1 {
		t.Errorf("Delete: %v, %+v, dropped %v", err, m.org, *dropped)
	}

	m.err = errors.New("down")
	*dropped = nil
	for name, err := range map[string]error{
		"personal": func() error { _, err := svc.SetPersonalAPIKeys(ctx, member, false, true); return err }(),
		"email":    func() error { _, err := svc.SetSecurityEmail(ctx, member, ""); return err }(),
		"template": func() error { _, err := svc.SetOIDCSubjectTemplate(ctx, member, ""); return err }(),
		"hosts":    func() error { _, err := svc.SetRedirectHosts(ctx, member, nil); return err }(),
		"survey":   func() error { _, err := svc.SetSurvey(ctx, member, Survey{}); return err }(),
		"delete":   svc.Delete(ctx, member, ""),
	} {
		if !errors.Is(err, m.err) {
			t.Errorf("%s with the store down = %v", name, err)
		}
	}
	if len(*dropped) != 0 {
		t.Errorf("a failed change dropped sessions: %v", *dropped)
	}
}

func TestUnitAuthorizationScheme(t *testing.T) {
	m := &mem{s: Defaults(), revoked: []kernel.ULID{kernel.NewULID()}}
	svc, dropped := newService(m, time.Now())
	if got, err := svc.SetAuthorizationScheme(t.Context(), kernel.Principal{}, accessapi.LoginPolicies, ""); err != nil || got != accessapi.LoginPolicies || len(*dropped) != 1 {
		t.Fatalf("switch = %s, %v; dropped %v", got, err, *dropped)
	}
	if _, err := svc.SetAuthorizationScheme(t.Context(), kernel.Principal{}, "UNKNOWN", ""); !errors.Is(err, ErrInvalid) || len(*dropped) != 1 {
		t.Fatalf("unknown strategy = %v, dropped %v", err, *dropped)
	}
	m.err = errors.New("failed transaction")
	if _, err := svc.SetAuthorizationScheme(t.Context(), kernel.Principal{}, accessapi.ManagedUsers, ""); !errors.Is(err, m.err) || len(*dropped) != 1 {
		t.Fatalf("failed switch = %v, dropped %v", err, *dropped)
	}
}

// templateDecider is the account admin and organization admin decision
// TestUnitOIDCSubjectTemplate sets, with the errors each answers.
type templateDecider struct {
	admin, orgAdmin    bool
	authzErr, admitErr error
	asked              accessapi.Action
	space              kernel.ULID
}

func (d *templateDecider) Authorize(_ context.Context, _ kernel.Principal, a accessapi.Action, space kernel.ULID) (bool, error) {
	d.asked, d.space = a, space
	return d.admin, d.authzErr
}

func (d *templateDecider) Admit(context.Context, orgadmin.Actor) error {
	switch {
	case d.admitErr != nil:
		return d.admitErr
	case !d.orgAdmin:
		return orgadmin.ErrForbidden
	}
	return nil
}

// TestUnitOIDCSubjectTemplate checks the read answers the stored template
// or the default to an account admin and an organization admin, and
// refuses everyone else and a principal of no account.
func TestUnitOIDCSubjectTemplate(t *testing.T) {
	ctx := t.Context()
	m := &mem{s: Defaults()}
	d := &templateDecider{admin: true}
	svc := NewService(Deps{Store: m, Authz: d, Admins: d})
	member := kernel.Principal{
		Kind: kernel.PrincipalUser, OrganizationID: kernel.NewOrganizationID(),
		AccountID: kernel.NewAccountID(), MemberID: kernel.NewMemberID(),
	}
	if tpl, err := svc.OIDCSubjectTemplate(ctx, member); err != nil || tpl != federation.DefaultSubjectTemplate ||
		d.asked != accessapi.ActionAccountAdmin || d.space != member.AccountID.ULID {
		t.Errorf("the default to an account admin = %q, %v, decided %s on %s", tpl, err, d.asked, d.space)
	}
	m.s.OIDCSubjectTemplate = "stack|{callerId}"
	*d = templateDecider{orgAdmin: true}
	if tpl, err := svc.OIDCSubjectTemplate(ctx, member); err != nil || tpl != "stack|{callerId}" {
		t.Errorf("the set template to an organization admin = %q, %v", tpl, err)
	}

	errDown := errors.New("down")
	for name, tc := range map[string]struct {
		d    templateDecider
		p    kernel.Principal
		want error
	}{
		"neither":     {templateDecider{}, member, ErrNotTemplateReader},
		"no account":  {templateDecider{admin: true, orgAdmin: true}, kernel.Principal{Kind: kernel.PrincipalBreakGlass}, ErrNotTemplateReader},
		"authz down":  {templateDecider{authzErr: errDown}, member, errDown},
		"admins down": {templateDecider{admitErr: errDown}, member, errDown},
		"store down":  {templateDecider{admin: true}, member, errDown},
	} {
		*d = tc.d
		m.err = nil
		if name == "store down" {
			m.err = errDown
		}
		if tpl, err := svc.OIDCSubjectTemplate(ctx, tc.p); !errors.Is(err, tc.want) || tpl != "" {
			t.Errorf("%s = %q, %v; want %v", name, tpl, err, tc.want)
		}
	}
}
