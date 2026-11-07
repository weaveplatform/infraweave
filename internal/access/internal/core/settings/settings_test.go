package settings

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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

func (m *mem) SetPersonalAPIKeys(
	_ context.Context,
	_ kernel.AccountID,
	enabled, disable bool,
) ([]kernel.ULID, error) {
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

func (m *mem) DeleteAccount(
	_ context.Context,
	_ kernel.AccountID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
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
