package pg_test

import (
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestAccContextCreate creates a context with its labels, hooks, elements
// and an attachment, holding its space and the stack, and refuses what a
// context cannot hold.
func TestAccContextCreate(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	file := base64.StdEncoding.EncodeToString([]byte("key = 1\n"))
	desc, mode := "the token", "600"
	c, err := f.svc.Create(ctx, f.admin, core.Input{
		Space: "team", Name: "Prod / EU", Labels: []string{"b", "a"},
		Hooks: map[string][]string{stacksapi.BeforePlan: {"echo 1"}, "unknown": {"x"}},
		Config: []core.ElementInput{
			variable("REGION", "eu-west-1"),
			{Name: "TOKEN", Value: new("s3cret"), WriteOnly: true, Description: &desc},
			{Name: "conf/app.toml", FileMount: true, Value: &file, FileMode: &mode},
		},
		Stacks: []core.StackInput{{Stack: "web", Priority: 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Slug != "prod-eu" || c.Space != f.sp["team"] || c.Version != 1 ||
		!reflect.DeepEqual(c.Labels, []string{"a", "b"}) ||
		!reflect.DeepEqual(c.Hooks, map[string][]string{stacksapi.BeforePlan: {"echo 1"}}) {
		t.Errorf("created %+v", c)
	}
	if !slices.Contains(f.spaces.holds, f.sp["team"]) ||
		!slices.Contains(f.stacks.holds, f.web.ID) {
		t.Errorf("holds: spaces %v, stacks %v", f.spaces.holds, f.stacks.holds)
	}
	got, err := f.svc.Context(ctx, f.acct, "prod-eu")
	if err != nil || got.ID != c.ID || !reflect.DeepEqual(got.Hooks, c.Hooks) {
		t.Errorf("Context = %+v, %v", got, err)
	}
	d, err := f.svc.Details(ctx, f.acct, got)
	if err != nil {
		t.Fatal(err)
	}
	es := d.Elements[c.ID]
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name
	}
	if !reflect.DeepEqual(names, []string{"REGION", "TOKEN", "conf/app.toml"}) {
		t.Fatalf("elements %v", names)
	}
	if es[0].Value == nil || *es[0].Value != "eu-west-1" || es[0].Checksum != sum("eu-west-1") ||
		!es[0].CreatedAt.Equal(f.now) {
		t.Errorf("REGION %+v", es[0])
	}
	if es[1].Value != nil || !es[1].WriteOnly || es[1].Checksum != sum("s3cret") ||
		es[1].Description != "the token" {
		t.Errorf("TOKEN %+v", es[1])
	}
	if !es[2].FileMount || *es[2].Value != file || *es[2].FileMode != "600" {
		t.Errorf("file %+v", es[2])
	}
	as := d.Attachments[c.ID]
	if len(as) != 1 || as[0].Stack != f.web.ID || as[0].Priority != 3 || as[0].Autoattached ||
		d.Stacks[f.web.ID] != f.web {
		t.Errorf("attachments %+v, stacks %+v", as, d.Stacks)
	}
	if got := f.changes(t, c.ID); !reflect.DeepEqual(got, []string{"created"}) {
		t.Errorf("events %v", got)
	}
	if _, err := f.svc.Create(
		ctx,
		f.admin,
		core.Input{Space: "team", Name: "prod eu"},
	); !isTaken(
		err,
	) {
		t.Errorf("a taken slug = %v", err)
	}

	big := base64.StdEncoding.EncodeToString(make([]byte, core.MaxFile+1))
	bad := map[string]core.Input{
		"/name":     {Name: " "},
		"/labels/0": {Name: "x", Labels: []string{" a"}},
		"/configAttachments/1/id": {
			Name:   "x",
			Config: []core.ElementInput{variable("A", "1"), variable("A", "2")},
		},
		"/configAttachments/0/value": {Name: "x", Config: []core.ElementInput{{Name: "A"}}},
		"/configAttachments/0/fileMode": {
			Name:   "x",
			Config: []core.ElementInput{{Name: "A", Value: new(""), FileMode: &mode}},
		},
	}
	bad["/name "] = core.Input{Name: "---"}
	for want, in := range bad {
		_, err := f.svc.Create(ctx, f.admin, in)
		if got := pointers(t, err); !slices.Contains(got, strings.TrimSpace(want)) {
			t.Errorf("%s: refused %v", want, got)
		}
	}
	elements := map[string]core.ElementInput{
		"/id":       {Name: ""},
		"/id ":      {Name: "A\x00"},
		"/id  ":     {Name: "A=B", Value: new("")},
		"/id   ":    {Name: "/etc/passwd", FileMount: true, Value: new("")},
		"/id    ":   {Name: "a/../b", FileMount: true, Value: new("")},
		"/id     ":  {Name: "a//b", FileMount: true, Value: new("")},
		"/id      ": {Name: ".", FileMount: true, Value: new("")},
		"/value":    {Name: "A", Value: new("a\x00b")},
		"/value ":   {Name: "f", FileMount: true, Value: new("not base64!")},
		"/value  ":  {Name: "f", FileMount: true, Value: &big},
		"/fileMode": {Name: "f", FileMount: true, Value: new(""), FileMode: new("rwx")},
	}
	for want, e := range elements {
		_, err := f.svc.Create(ctx, f.admin, core.Input{Name: "x", Config: []core.ElementInput{e}})
		if got := pointers(
			t,
			err,
		); !slices.Contains(
			got,
			"/configAttachments/0"+strings.TrimSpace(want),
		) {
			t.Errorf("%q: refused %v", want, got)
		}
	}
	if _, err := f.svc.Create(
		ctx,
		f.admin,
		core.Input{Space: "nope", Name: "x"},
	); !missing(
		err,
		"space",
	) {
		t.Errorf("a missing space = %v", err)
	}
	if _, err := f.svc.Create(
		ctx,
		f.reader,
		core.Input{Space: "hidden", Name: "x"},
	); !missing(
		err,
		"space",
	) {
		t.Errorf("a hidden space = %v", err)
	}
	for stack, want := range map[string]string{"nope": "stack"} {
		_, err := f.svc.Create(ctx, f.admin, core.Input{
			Space: "team", Name: "y",
			Stacks: []core.StackInput{{Stack: stack}},
		})
		if !missing(err, want) {
			t.Errorf("attaching %s = %v", stack, err)
		}
	}
	_, err = f.svc.Create(ctx, f.writer, core.Input{
		Space: "team", Name: "y",
		Stacks: []core.StackInput{{Stack: "db"}},
	})
	if !missing(err, "stack") {
		t.Errorf("attaching a hidden stack = %v", err)
	}
	_, err = f.svc.Create(ctx, f.reader, core.Input{
		Space: "team", Name: "y",
		Stacks: []core.StackInput{{Stack: "web"}},
	})
	if !denied(err, accessapi.ActionStackContextAttach) {
		t.Errorf("attaching without the action = %v", err)
	}
	_, err = f.svc.Create(ctx, f.admin, core.Input{
		Space: "team", Name: "y",
		Stacks: []core.StackInput{{Stack: "db"}},
	})
	if got := pointers(t, err); !reflect.DeepEqual(got, []string{"/stackAttachments/0/stack"}) {
		t.Errorf("attaching an unreachable stack: %v", got)
	}
	if _, err := f.svc.Context(ctx, f.acct, "y"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("a refused create left %v", err)
	}
}

// TestAccContextUpdate replaces a context's fields, its elements only
// when asked, moves it within its stacks' reach and refuses a move out
// of it, and checks If-Match.
func TestAccContextUpdate(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	c := f.create(t, "Shared", "team", variable("A", "1"))
	if _, _, err := f.svc.Attach(ctx, f.admin, "shared", "web", 0); err != nil {
		t.Fatal(err)
	}
	in := core.Input{
		Space: "team", Name: "Shared Two", Labels: []string{"x"},
		Config: []core.ElementInput{variable("B", "2")},
	}
	got, err := f.svc.Update(ctx, f.writer, "shared", kernel.ExpectVersions(2), in, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "shared" || got.Name != "Shared Two" || got.Version != 3 ||
		!reflect.DeepEqual(got.Labels, []string{"x"}) {
		t.Errorf("updated %+v", got)
	}
	if e, err := f.svc.Element(ctx, f.acct, "shared", "A"); err != nil || *e.Value != "1" {
		t.Errorf("A after an update that keeps the elements = %+v, %v", e, err)
	}
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		"shared",
		kernel.ExpectVersions(2),
		in,
		true,
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("a stale update = %v", err)
	}
	if _, err := f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Element(ctx, f.acct, "shared", "A"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("A after replacing the elements = %v", err)
	}
	if e, err := f.svc.Element(ctx, f.acct, "shared", "B"); err != nil || *e.Value != "2" {
		t.Errorf("B = %+v, %v", e, err)
	}
	in.Config = []core.ElementInput{{Name: "C"}}
	if got := pointers(t, func() error {
		_, err := f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, true)
		return err
	}()); !reflect.DeepEqual(got, []string{"/input/configAttachments/0/value"}) {
		t.Errorf("an element with no value: %v", got)
	}
	in.Config = nil
	in.Stacks = []core.StackInput{{Stack: "web"}}
	if got := pointers(t, func() error {
		_, err := f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, false)
		return err
	}()); !reflect.DeepEqual(got, []string{"/input/stackAttachments"}) {
		t.Errorf("attachments in an update: %v", got)
	}
	in.Stacks = nil

	in.Space = "app"
	if _, err := f.svc.Update(
		ctx,
		f.writer,
		"shared",
		kernel.Expect{},
		in,
		false,
	); !denied(
		err,
		accessapi.ActionContextCreate,
	) {
		t.Errorf("a move without CONTEXT_CREATE = %v", err)
	}
	if got, err := f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, false); err != nil ||
		got.Space != f.sp["app"] {
		t.Errorf("a move into the stack's space = %+v, %v", got, err)
	}
	in.Space = "leaf"
	_, err = f.svc.Update(ctx, f.admin, "shared", kernel.Expect{}, in, false)
	if got := pointers(t, err); !reflect.DeepEqual(got, []string{"/input/space"}) ||
		!strings.Contains(err.Error(), "web") {
		t.Errorf("a move out of web's reach: %v, %v", got, err)
	}
	if _, err := f.svc.Update(
		ctx,
		f.admin,
		"nope",
		kernel.Expect{},
		in,
		false,
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("a missing context = %v", err)
	}
	if got := f.changes(t, c.ID); !reflect.DeepEqual(got,
		[]string{"created", "attached", "updated", "updated", "updated"}) {
		t.Errorf("events %v", got)
	}
}

// TestAccContextElements adds, replaces, reads and deletes config
// elements: a replace keeps the row and a value it is not sent, a
// write-only value is never read back, and the checksum always is.
func TestAccContextElements(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	c := f.create(t, "Shared", "team")
	set := func(in core.ElementInput) (core.Element, bool, error) {
		return f.svc.SetElement(ctx, f.writer, "shared", in, "", "/configId")
	}
	e, created, err := set(core.ElementInput{Name: "TOKEN", Value: new("s3cret"), WriteOnly: true})
	if err != nil || !created || e.Value != nil || e.Checksum != sum("s3cret") || !e.WriteOnly {
		t.Fatalf("created %+v, %v, %v", e, created, err)
	}
	f.now = f.now.Add(time.Second)
	desc := "rotated"
	again, created, err := set(
		core.ElementInput{Name: "TOKEN", WriteOnly: true, Description: &desc},
	)
	if err != nil || created || again.ID != e.ID || again.Checksum != e.Checksum ||
		again.Description != "rotated" || !again.CreatedAt.Equal(e.CreatedAt) {
		t.Errorf("kept %+v, %v, %v", again, created, err)
	}
	var sealed []byte
	if err := f.db.QueryRow(
		ctx,
		`SELECT value_sealed FROM config_elements WHERE id = $1`,
		e.ID,
	).Scan(&sealed); err != nil || len(sealed) == 0 || strings.Contains(string(sealed), "s3cret") {
		t.Errorf("stored %q, %v", sealed, err)
	}
	if _, _, err := set(
		core.ElementInput{Name: "TOKEN"},
	); !reflect.DeepEqual(
		pointers(t, err),
		[]string{"/value"},
	) {
		t.Errorf("making a write-only value readable without it = %v", err)
	}
	if _, _, err := set(
		core.ElementInput{Name: "TOKEN", FileMount: true, WriteOnly: true},
	); err == nil {
		t.Error("changing the type without a value was accepted")
	}
	if _, _, err := set(core.ElementInput{Name: "NEW"}); err == nil {
		t.Error("a new element with no value was accepted")
	}
	plain, created, err := set(core.ElementInput{Name: "TOKEN", Value: new("open")})
	if err != nil || created || *plain.Value != "open" || plain.Checksum != sum("open") ||
		!plain.CreatedAt.Equal(f.now) || plain.ID != e.ID {
		t.Errorf("replaced %+v, %v, %v", plain, created, err)
	}
	if _, _, err := set(
		core.ElementInput{Name: "/abs", FileMount: true, Value: new("")},
	); !reflect.DeepEqual(
		pointers(t, err),
		[]string{"/configId"},
	) {
		t.Errorf("a bad path = %v", err)
	}
	if _, _, err := f.svc.SetElement(
		ctx,
		f.writer,
		"nope",
		core.ElementInput{Name: "A", Value: new("")},
		"",
		"/id",
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("a missing context = %v", err)
	}
	if _, err := f.svc.Element(ctx, f.acct, "nope", "A"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("an element of a missing context = %v", err)
	}
	gone, err := f.svc.DeleteElement(ctx, f.writer, "shared", "TOKEN")
	if err != nil || gone.ID != e.ID {
		t.Errorf("deleted %+v, %v", gone, err)
	}
	if _, err := f.svc.DeleteElement(
		ctx,
		f.writer,
		"shared",
		"TOKEN",
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("deleting it again = %v", err)
	}
	if got := f.changes(t, c.ID); !reflect.DeepEqual(got,
		[]string{"created", "configured", "configured", "configured", "configured"}) {
		t.Errorf("events %v", got)
	}
}

// TestAccSealedValue opens a value only in its own row: a ciphertext
// copied to another element fails to open.
func TestAccSealedValue(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.create(t, "Shared", "team", variable("A", "alpha"), variable("B", "beta"))
	if _, err := f.db.Exec(ctx, `UPDATE config_elements SET value_sealed =
		(SELECT value_sealed FROM config_elements WHERE element = 'A') WHERE element = 'B'`); err != nil {
		t.Fatal(err)
	}
	if e, err := f.svc.Element(ctx, f.acct, "shared", "A"); err != nil || *e.Value != "alpha" {
		t.Errorf("A = %+v, %v", e, err)
	}
	if _, err := f.svc.Element(ctx, f.acct, "shared", "B"); err == nil ||
		!strings.Contains(err.Error(), "open element B") {
		t.Errorf("B with A's ciphertext = %v", err)
	}
}

// TestAccContextAttach attaches within a stack's reach, through spaces
// that inherit, changes a priority in place, refuses a context out of
// reach or hidden, and detaches.
func TestAccContextAttach(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	top := f.create(t, "Top", "")
	team := f.create(t, "Team", "team")
	f.create(t, "Leaf", "leaf")
	f.create(t, "Hidden", "hidden")
	a, st, err := f.svc.Attach(ctx, f.admin, "top", "web", 5)
	if err != nil || st != f.web || a.Priority != 5 || a.Stack != f.web.ID || a.Context != top.ID {
		t.Fatalf("attached %+v to %+v, %v", a, st, err)
	}
	again, _, err := f.svc.Attach(ctx, f.admin, "top", "web", 1)
	if err != nil || again.ID != a.ID || again.Priority != 1 {
		t.Errorf("reattached %+v, %v", again, err)
	}
	if _, _, err := f.svc.Attach(ctx, f.writer, "team", "web", 0); err != nil {
		t.Errorf("attaching team = %v", err)
	}
	for slug, stack := range map[string]string{"leaf": "web", "hidden": "web", "team": "db"} {
		_, _, err := f.svc.Attach(ctx, f.admin, slug, stack, 0)
		if got := pointers(t, err); !reflect.DeepEqual(got, []string{"/stackId"}) {
			t.Errorf("attaching %s to %s: %v", slug, stack, got)
		}
	}
	if _, _, err := f.svc.Attach(ctx, f.writer, "hidden", "web", 0); !missing(err, "context") {
		t.Errorf("attaching a hidden context = %v", err)
	}
	if _, _, err := f.svc.Attach(ctx, f.admin, "top", "nope", 0); !missing(err, "stack") {
		t.Errorf("attaching to a missing stack = %v", err)
	}
	if _, _, err := f.svc.Attach(
		ctx,
		f.admin,
		"nope",
		"web",
		0,
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("attaching a missing context = %v", err)
	}

	got, err := f.svc.AttachedContexts(ctx, f.acct, []kernel.ULID{f.web.ID, f.data.ID})
	if err != nil || len(got) != 1 || len(got[f.web.ID]) != 2 ||
		got[f.web.ID][0].Context != "team" || got[f.web.ID][1].Context != "top" {
		t.Errorf("AttachedContexts = %+v, %v", got, err)
	}
	if sp, err := f.svc.AttachmentSpaceOf(
		ctx,
		f.acct,
		a.ID.String(),
	); err != nil ||
		sp != f.sp["app"] {
		t.Errorf("AttachmentSpaceOf = %v, %v", sp, err)
	}
	for _, id := range []string{"nope", kernel.NewULID().String()} {
		if _, err := f.svc.AttachmentSpaceOf(
			ctx,
			f.acct,
			id,
		); !errors.Is(
			err,
			accessapi.ErrNoSubject,
		) {
			t.Errorf("AttachmentSpaceOf %s = %v", id, err)
		}
	}
	if sp, err := f.svc.SpaceOf(ctx, f.acct, "team"); err != nil || sp != f.sp["team"] {
		t.Errorf("SpaceOf = %v, %v", sp, err)
	}
	if _, err := f.svc.SpaceOf(ctx, f.acct, "nope"); !errors.Is(err, accessapi.ErrNoSubject) {
		t.Errorf("SpaceOf a missing context = %v", err)
	}
	if as, st, err := f.svc.AttachedStack(ctx, f.acct, "top", a.ID.String()); err != nil ||
		as.ID != a.ID || st != f.web {
		t.Errorf("AttachedStack = %+v, %+v, %v", as, st, err)
	}
	if _, _, err := f.svc.AttachedStack(
		ctx,
		f.acct,
		"team",
		a.ID.String(),
	); !missing(
		err,
		"attachment",
	) {
		t.Errorf("another context's attachment = %v", err)
	}
	if _, _, err := f.svc.AttachedStack(
		ctx,
		f.acct,
		"nope",
		a.ID.String(),
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("an attachment of a missing context = %v", err)
	}

	gone, st, err := f.svc.Detach(ctx, f.admin, a.ID.String())
	if err != nil || gone.ID != a.ID || st != f.web {
		t.Errorf("detached %+v, %+v, %v", gone, st, err)
	}
	if _, _, err := f.svc.Detach(ctx, f.admin, a.ID.String()); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("detaching again = %v", err)
	}
	if got := f.changes(t, top.ID); !reflect.DeepEqual(got,
		[]string{"created", "attached", "attached", "detached"}) {
		t.Errorf("events %v", got)
	}
	_ = team
}

// TestAccContextDelete refuses an attached context without force and
// deletes it with force, its elements and attachments with it.
func TestAccContextDelete(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	c := f.create(t, "Shared", "team", variable("A", "1"))
	if _, _, err := f.svc.Attach(ctx, f.admin, "shared", "web", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Delete(ctx, f.admin, "shared", kernel.Expect{}, false); err == nil ||
		!strings.Contains(err.Error(), "attached to 1 stacks") {
		t.Errorf("deleting an attached context = %v", err)
	}
	if _, err := f.svc.Delete(
		ctx,
		f.admin,
		"shared",
		kernel.ExpectVersions(1),
		true,
	); !errors.Is(
		err,
		kernel.ErrStale,
	) {
		t.Errorf("a stale delete = %v", err)
	}
	gone, err := f.svc.Delete(ctx, f.admin, "shared", kernel.Expect{}, true)
	if err != nil || gone.ID != c.ID || gone.Version != 3 {
		t.Errorf("deleted %+v, %v", gone, err)
	}
	var n int
	if err := f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM config_elements) +
		(SELECT count(*) FROM context_attachments)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows left %d, %v", n, err)
	}
	if _, err := f.svc.Delete(
		ctx,
		f.admin,
		"shared",
		kernel.Expect{},
		true,
	); !errors.Is(
		err,
		core.ErrNotFound,
	) {
		t.Errorf("deleting it again = %v", err)
	}
	if got := f.changes(
		t,
		c.ID,
	); !reflect.DeepEqual(
		got,
		[]string{"created", "attached", "deleted"},
	) {
		t.Errorf("events %v", got)
	}
}

// TestAccContextSearch searches and suggests the contexts the caller may
// read, and those a stack in a space may attach.
func TestAccContextSearch(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	for _, c := range []struct{ name, space string }{
		{"Top", ""}, {"Team", "team"}, {"App", "app"}, {"Leaf", "leaf"}, {"Hidden", "hidden"},
	} {
		f.create(t, c.name, c.space)
	}
	slugs := func(r search.Result[core.Context]) []string {
		out := make([]string, 0, len(r.Edges))
		for _, e := range r.Edges {
			out = append(out, e.Node.Slug)
		}
		return out
	}
	res, err := f.svc.Search(ctx, f.admin, search.Input{})
	if err != nil ||
		!reflect.DeepEqual(slugs(res), []string{"app", "hidden", "leaf", "team", "top"}) {
		t.Errorf("admin's search = %v, %v", slugs(res), err)
	}
	res, err = f.svc.Search(ctx, f.reader, search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"app", "team"}) {
		t.Errorf("reader's search = %v, %v", slugs(res), err)
	}
	res, sp, err := f.svc.Attachable(ctx, f.admin, "app", search.Input{})
	if err != nil || sp != f.sp["app"] ||
		!reflect.DeepEqual(slugs(res), []string{"app", "team", "top"}) {
		t.Errorf("attachable in app = %v, %v", slugs(res), err)
	}
	res, _, err = f.svc.Attachable(ctx, f.writer, "app", search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"app", "team"}) {
		t.Errorf("attachable in app for the writer = %v, %v", slugs(res), err)
	}
	res, _, err = f.svc.Attachable(ctx, f.admin, "leaf", search.Input{})
	if err != nil || !reflect.DeepEqual(slugs(res), []string{"leaf"}) {
		t.Errorf("attachable in leaf = %v, %v", slugs(res), err)
	}
	if _, _, err := f.svc.Attachable(
		ctx,
		f.reader,
		"hidden",
		search.Input{},
	); !missing(
		err,
		"space",
	) {
		t.Errorf("attachable in a hidden space = %v", err)
	}
	got, err := f.svc.Suggest(ctx, f.reader, search.SuggestionsInput{})
	if err != nil || len(got.Fields) == 0 {
		t.Errorf("Suggest = %+v, %v", got, err)
	}
	got, err = f.svc.AttachableSuggest(ctx, f.admin, "app", search.SuggestionsInput{})
	if err != nil || len(got.Fields) == 0 {
		t.Errorf("AttachableSuggest = %+v, %v", got, err)
	}
	if _, err := f.svc.AttachableSuggest(
		ctx,
		f.admin,
		"nope",
		search.SuggestionsInput{},
	); !missing(
		err,
		"space",
	) {
		t.Errorf("AttachableSuggest in a missing space = %v", err)
	}
	f.spaces.err = errors.New("down")
	if _, err := f.svc.Search(ctx, f.admin, search.Input{}); err == nil {
		t.Error("a search with the tree down succeeded")
	}
	if _, _, err := f.svc.Attachable(ctx, f.admin, "app", search.Input{}); err == nil {
		t.Error("an attachable search with the spaces down succeeded")
	}
}

// TestAccContextGuards refuses deleting a space that holds a context and
// moving a space or a stack out of an attached context's reach, deletes a
// deleted stack's attachments and elements, and reads a stack's contexts
// and elements for its view.
func TestAccContextGuards(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.create(t, "Team", "team", variable("A", "1"), core.ElementInput{
		Name: "S", Value: new("secret"), WriteOnly: true,
	})
	f.create(t, "App", "app")
	for _, slug := range []string{"team", "app"} {
		if _, _, err := f.svc.Attach(ctx, f.admin, slug, "web", 0); err != nil {
			t.Fatal(err)
		}
	}
	space := f.svc.SpaceGuard()
	for slug, want := range map[string]bool{"team": true, "hidden": false} {
		if got, err := space.SpaceInUse(ctx, f.acct, f.sp[slug]); err != nil || got != want {
			t.Errorf("SpaceInUse %s = %v, %v", slug, got, err)
		}
	}
	lost, err := space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct,
		Space:   f.sp["app"],
		Subtree: []kernel.ULID{f.sp["leaf"]},
		From: []kernel.ULID{
			f.sp["team"],
			f.acct.ULID,
		},
		To: []kernel.ULID{f.sp["hidden"], f.acct.ULID},
	})
	if err != nil ||
		!reflect.DeepEqual(lost, []accessapi.LostReach{{Stack: "web", Entity: "context team"}}) {
		t.Errorf("moving app below hidden: %+v, %v", lost, err)
	}
	lost, err = space.LostOnMove(ctx, accessapi.SpaceMove{
		Account: f.acct, Space: f.sp["leaf"], From: []kernel.ULID{f.sp["app"]},
		To: []kernel.ULID{f.sp["team"]},
	})
	if err != nil || len(lost) != 0 {
		t.Errorf("moving leaf, which holds no stack: %+v, %v", lost, err)
	}

	stack := f.svc.StackGuard()
	moved, err := stack.LostOnMove(ctx, stacksapi.StackMove{Stack: f.web, To: f.sp["hidden"]})
	if err != nil || !reflect.DeepEqual(moved, []string{"context app", "context team"}) {
		t.Errorf("moving web to hidden: %v, %v", moved, err)
	}
	if moved, err := stack.LostOnMove(
		ctx,
		stacksapi.StackMove{Stack: f.web, To: f.sp["team"]},
	); err != nil ||
		!reflect.DeepEqual(moved, []string{"context app"}) {
		t.Errorf("moving web to team: %v, %v", moved, err)
	}
	if moved, err := stack.LostOnMove(
		ctx,
		stacksapi.StackMove{Stack: f.data, To: f.sp["team"]},
	); err != nil ||
		len(moved) != 0 {
		t.Errorf("moving db, which has no context: %v, %v", moved, err)
	}

	own := core.Element{
		ID: kernel.NewULID(), Account: f.acct, Owner: core.Owner{Stack: f.web.ID},
		Name: "OWN", Value: new("mine"), Checksum: "c", CreatedAt: f.now,
	}
	if err := f.store.PutElement(ctx, own); err != nil {
		t.Fatal(err)
	}
	config, err := f.svc.Config(ctx, f.acct, []kernel.ULID{f.web.ID, f.data.ID})
	if err != nil || len(config) != 1 || len(config[f.web.ID]) != 1 ||
		*config[f.web.ID][0].Value != "mine" {
		t.Errorf("Config = %+v, %v", config, err)
	}
	attached, err := f.svc.AttachedContexts(ctx, f.acct, []kernel.ULID{f.web.ID})
	if err != nil || len(attached[f.web.ID]) != 2 {
		t.Fatalf("AttachedContexts = %+v, %v", attached, err)
	}
	team := attached[f.web.ID][1]
	if team.Context != "team" || len(team.Config) != 2 || *team.Config[0].Value != "1" ||
		team.Config[1].Value != nil || team.Config[1].Checksum != sum("secret") {
		t.Errorf("team as web sees it: %+v", team)
	}
	for _, empty := range []func() (int, error){
		func() (int, error) { m, err := f.svc.Config(ctx, f.acct, nil); return len(m), err },
		func() (int, error) { m, err := f.svc.AttachedContexts(ctx, f.acct, nil); return len(m), err },
	} {
		if n, err := empty(); n != 0 || err != nil {
			t.Errorf("no stacks: %d, %v", n, err)
		}
	}

	if err := stack.Deleting(ctx, f.web); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM config_elements WHERE stack_id IS NOT NULL) +
		(SELECT count(*) FROM context_attachments)`).
		Scan(&n); err != nil ||
		n != 0 {
		t.Errorf("rows of the deleted stack %d, %v", n, err)
	}
	if lost, err := space.LostOnMove(
		ctx,
		accessapi.SpaceMove{Account: f.acct, Space: f.sp["app"]},
	); err != nil ||
		len(lost) != 0 {
		t.Errorf("a move with nothing attached: %+v, %v", lost, err)
	}
}

func isTaken(err error) bool {
	_, ok := errors.AsType[*core.SlugTaken](err)
	return ok
}

func denied(err error, action accessapi.Action) bool {
	d, ok := errors.AsType[*core.Denied](err)
	return ok && d.Action == action
}
