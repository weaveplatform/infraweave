package operator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/account"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/breakglass"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

var errDown = errors.New("down")

// hash is one valid hash for every test: New runs 600,000 iterations, too
// slow to repeat per case.
var hash = sync.OnceValue(func() string {
	h, err := breakglass.New("correct horse")
	if err != nil {
		panic(err)
	}
	return h.String()
})

// mem is Store in memory. Each organization's live sessions are the IDs in
// sessions; err fails every write.
type mem struct {
	orgs     map[kernel.OrganizationID]Organization
	sessions map[kernel.OrganizationID][]kernel.ULID
	entries  []kernel.OrganizationAuditEntry
	err      error
}

func newMem() *mem {
	return &mem{
		orgs:     map[kernel.OrganizationID]Organization{},
		sessions: map[kernel.OrganizationID][]kernel.ULID{},
	}
}

func (m *mem) CreateOrganization(
	_ context.Context,
	org organization.Organization,
	first account.Account,
	bg *breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	if m.err != nil {
		return m.err
	}
	for _, o := range m.orgs {
		if o.Slug == org.Slug {
			return port.ErrSlugTaken
		}
	}
	m.orgs[org.ID] = Organization{
		Organization: org,
		Accounts:     []account.Account{first},
		BreakGlass:   bg,
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *mem) Organizations(
	_ context.Context,
	after kernel.OrganizationID,
	limit int,
) ([]Organization, error) {
	var out []Organization
	for _, o := range m.orgs {
		if after.IsZero() || o.ID.String() > after.String() {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b Organization) int {
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out[:min(len(out), limit)], nil
}

func (m *mem) OrganizationBySlug(_ context.Context, slug kernel.Slug) (Organization, error) {
	for _, o := range m.orgs {
		if o.Slug == slug {
			return o, nil
		}
	}
	return Organization{}, port.ErrNotFound
}

func (m *mem) DeleteOrganization(
	_ context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	if m.err != nil {
		return nil, m.err
	}
	delete(m.orgs, id)
	m.entries = append(m.entries, e)
	return m.sessions[id], nil
}

func (m *mem) SetBreakGlass(
	_ context.Context,
	id kernel.OrganizationID,
	c breakglass.Credential,
	e kernel.OrganizationAuditEntry,
) error {
	if m.err != nil {
		return m.err
	}
	o := m.orgs[id]
	o.BreakGlass = &c
	m.orgs[id] = o
	m.entries = append(m.entries, e)
	return nil
}

func (m *mem) DeleteBreakGlass(
	_ context.Context,
	id kernel.OrganizationID,
	e kernel.OrganizationAuditEntry,
) error {
	if m.err != nil {
		return m.err
	}
	o := m.orgs[id]
	o.BreakGlass = nil
	m.orgs[id] = o
	m.entries = append(m.entries, e)
	return nil
}

type world struct {
	m       *mem
	s       *Service
	dropped []kernel.ULID
	by      Actor
}

func newWorld() *world {
	w := &world{m: newMem()}
	w.s = NewService(Deps{
		Store: w.m,
		Drop:  func(id kernel.ULID) { w.dropped = append(w.dropped, id) },
		Now:   func() time.Time { return t0 },
	})
	w.by = Actor{
		Principal: kernel.Principal{Kind: kernel.PrincipalOperator, ActorID: kernel.NewULID()},
		RemoteIP:  "192.0.2.1",
	}
	return w
}

func input(slug string) Input {
	return Input{
		Slug: slug, Name: "Acme", AccountSlug: "main", AccountName: "Main",
		BreakGlass: &BreakGlassInput{Login: " root-admin ", PasswordHash: hash()},
	}
}

func TestUnitCreate(t *testing.T) {
	w := newWorld()
	o, err := w.s.Create(t.Context(), w.by, input("acme"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Slug != "acme" || len(o.Accounts) != 1 || o.Accounts[0].Slug != "main" ||
		o.Accounts[0].Claimed() || !o.CreatedAt.Equal(t0) {
		t.Errorf("organization = %+v", o)
	}
	if o.BreakGlass == nil || o.BreakGlass.Login != "root-admin" ||
		len(o.BreakGlass.Hash.Key) != 0 {
		t.Errorf("break glass = %+v, want the trimmed login and no hash", o.BreakGlass)
	}
	stored := w.m.orgs[o.ID].BreakGlass
	if stored == nil || !stored.Hash.Verify("correct horse") {
		t.Errorf("stored credential = %+v, want the hash", stored)
	}
	e := w.m.entries[0]
	if e.Action != ActionCreate || e.EventType != "CREATE" ||
		e.ActorKind != kernel.PrincipalOperator ||
		e.ActorID != w.by.Principal.ActorID.String() || e.RemoteIP != "192.0.2.1" ||
		e.AffectedResource.Type != ResourceOrganization ||
		e.AffectedResource.ID != "acme" {
		t.Errorf("entry = %+v", e)
	}
	if strings.Contains(string(e.Body), "pbkdf2") || !strings.Contains(string(e.Body), `"main"`) {
		t.Errorf("body = %s, want the account and no hash", e.Body)
	}

	in := input("bare")
	in.BreakGlass = nil
	o, err = w.s.Create(t.Context(), w.by, in)
	if err != nil || o.BreakGlass != nil {
		t.Errorf("Create without break glass = %+v, %v", o.BreakGlass, err)
	}
	if _, err := w.s.Create(t.Context(), w.by, input("acme")); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("duplicate slug = %v, want ErrSlugTaken", err)
	}
}

func TestUnitCreateInvalid(t *testing.T) {
	w := newWorld()
	in := Input{
		Slug: "Acme", Name: "", AccountSlug: "root",
		BreakGlass: &BreakGlassInput{Login: "", PasswordHash: "$pbkdf2-sha256$i=1000$AAAA$AAAA"},
	}
	_, err := w.s.Create(t.Context(), w.by, in)
	if !errors.Is(err, organization.ErrInvalid) || !errors.Is(err, breakglass.ErrInvalid) {
		t.Fatalf("Create = %v, want both kinds of invalid", err)
	}
	for _, field := range []string{"slug", "name", "account.slug", "breakGlass.login", "breakGlass.passwordHash"} {
		if !strings.Contains(err.Error(), field+":") {
			t.Errorf("error %q does not name %s", err, field)
		}
	}
	if len(w.m.orgs) != 0 || len(w.m.entries) != 0 {
		t.Error("invalid input reached the store")
	}
}

func TestUnitListGetDelete(t *testing.T) {
	w := newWorld()
	a, _ := w.s.Create(t.Context(), w.by, input("acme"))
	if _, err := w.s.Create(t.Context(), w.by, input("beta")); err != nil {
		t.Fatal(err)
	}
	page, err := w.s.List(t.Context(), kernel.OrganizationID{}, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("List = %d, %v", len(page), err)
	}
	rest, _ := w.s.List(t.Context(), page[0].ID, 10)
	if len(rest) != 1 || rest[0].ID == page[0].ID {
		t.Errorf("second page = %+v", rest)
	}

	if got, err := w.s.Get(t.Context(), "acme"); err != nil || got.ID != a.ID {
		t.Errorf("Get = %+v, %v", got, err)
	}
	for _, slug := range []string{"nobody", "Not A Slug"} {
		if _, err := w.s.Get(t.Context(), slug); !errors.Is(err, port.ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", slug, err)
		}
	}

	live := []kernel.ULID{kernel.NewULID(), kernel.NewULID()}
	w.m.sessions[a.ID] = live
	got, err := w.s.Delete(t.Context(), w.by, "acme")
	if err != nil || got.ID != a.ID {
		t.Fatalf("Delete = %+v, %v", got, err)
	}
	if !slices.Equal(w.dropped, live) {
		t.Errorf("dropped %v, want %v", w.dropped, live)
	}
	if e := w.m.entries[len(w.m.entries)-1]; e.Action != ActionDelete || e.EventType != "DELETE" {
		t.Errorf("entry = %+v", e)
	}
	if _, err := w.s.Delete(t.Context(), w.by, "acme"); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestUnitBreakGlass(t *testing.T) {
	w := newWorld()
	in := input("acme")
	in.BreakGlass = nil
	if _, err := w.s.Create(t.Context(), w.by, in); err != nil {
		t.Fatal(err)
	}
	o, err := w.s.SetBreakGlass(t.Context(), w.by, "acme",
		BreakGlassInput{Login: "admin", PasswordHash: hash()})
	if err != nil || o.BreakGlass == nil || o.BreakGlass.Login != "admin" ||
		!o.BreakGlass.UpdatedAt.Equal(t0) || len(o.BreakGlass.Hash.Key) != 0 {
		t.Fatalf("SetBreakGlass = %+v, %v", o.BreakGlass, err)
	}
	e := w.m.entries[len(w.m.entries)-1]
	if e.Action != ActionBreakGlassSet || string(e.Body) != `{"login":"admin"}` {
		t.Errorf("entry = %+v", e)
	}

	_, err = w.s.SetBreakGlass(t.Context(), w.by, "acme",
		BreakGlassInput{Login: "admin", PasswordHash: "$argon2id$x"})
	if !errors.Is(err, breakglass.ErrInvalid) || !strings.Contains(err.Error(), "passwordHash:") {
		t.Errorf("SetBreakGlass with a bad hash = %v", err)
	}
	if _, err := w.s.SetBreakGlass(t.Context(), w.by, "nobody",
		BreakGlassInput{Login: "admin", PasswordHash: hash()}); !errors.Is(err, port.ErrNotFound) {
		t.Errorf("SetBreakGlass on no organization = %v", err)
	}

	o, err = w.s.DeleteBreakGlass(t.Context(), w.by, "acme")
	if err != nil || o.BreakGlass != nil || w.m.orgs[o.ID].BreakGlass != nil {
		t.Errorf("DeleteBreakGlass = %+v, %v", o.BreakGlass, err)
	}
	if e := w.m.entries[len(w.m.entries)-1]; e.Action != ActionBreakGlassUnset {
		t.Errorf("entry = %+v", e)
	}
	if _, err := w.s.DeleteBreakGlass(
		t.Context(),
		w.by,
		"nobody",
	); !errors.Is(
		err,
		port.ErrNotFound,
	) {
		t.Errorf("DeleteBreakGlass on no organization = %v", err)
	}
}

func TestUnitStoreFails(t *testing.T) {
	w := newWorld()
	if _, err := w.s.Create(t.Context(), w.by, input("acme")); err != nil {
		t.Fatal(err)
	}
	w.m.err = errDown
	bg := BreakGlassInput{Login: "admin", PasswordHash: hash()}
	for name, call := range map[string]func() error{
		"Create": func() error { _, err := w.s.Create(t.Context(), w.by, input("beta")); return err },
		"Delete": func() error { _, err := w.s.Delete(t.Context(), w.by, "acme"); return err },
		"SetBreakGlass": func() error {
			_, err := w.s.SetBreakGlass(t.Context(), w.by, "acme", bg)
			return err
		},
		"DeleteBreakGlass": func() error {
			_, err := w.s.DeleteBreakGlass(t.Context(), w.by, "acme")
			return err
		},
	} {
		if err := call(); !errors.Is(err, errDown) {
			t.Errorf("%s = %v, want the store's error", name, err)
		}
	}
	if len(w.dropped) != 0 {
		t.Errorf("a failed delete dropped %v", w.dropped)
	}
}
