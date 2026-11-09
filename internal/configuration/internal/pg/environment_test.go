package pg_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
)

// TestAccResolve resolves a stack's environment across its own elements,
// contexts attached by hand and one attached automatically, opening every
// value, a write-only one too, and ordering each phase's hooks.
func TestAccResolve(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.stacks.hooks["web"] = map[string][]string{"before_plan": {"stack"}, "after_plan": {"stack"}}
	for _, e := range []core.ElementInput{
		{Name: "TOKEN", Value: new("s3cret"), WriteOnly: true}, variable("REGION", "eu"),
	} {
		if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", e, "", "/configId"); err != nil {
			t.Fatal(err)
		}
	}
	hooks := func(name string) map[string][]string {
		return map[string][]string{"before_plan": {name}, "after_plan": {name}}
	}
	for _, c := range []core.Input{
		{
			Space: "team", Name: "Team", Hooks: hooks("team"),
			Config: []core.ElementInput{variable("REGION", "us"), variable("ZONE", "a")},
			Stacks: []core.StackInput{{Stack: "web", Priority: 1}},
		},
		{
			Name: "Top", Hooks: hooks("top"),
			Config: []core.ElementInput{
				variable("ZONE", "b"),
				{Name: "app.toml", FileMount: true, Value: new("a2V5"), WriteOnly: true},
			},
			Stacks: []core.StackInput{{Stack: "web"}},
		},
		{
			Space: "team", Name: "Auto", Labels: []string{"autoattach:*"}, Hooks: hooks("auto"),
			Config: []core.ElementInput{variable("LEVEL", "x"), variable("ZONE", "c")},
		},
	} {
		if _, err := f.svc.Create(ctx, f.admin, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.svc.Resolve(ctx, f.acct, f.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	elements := make([]string, 0, len(got.Elements))
	for _, e := range got.Elements {
		elements = append(elements, fmt.Sprintf("%s=%s %s:%s %t %v", e.Name, e.Value.Reveal(),
			e.Source, e.Context, e.WriteOnly, e.Value))
	}
	if want := []string{
		"LEVEL=x autoattached:auto false [redacted]",
		"REGION=eu stack: false [redacted]",
		"TOKEN=s3cret stack: true [redacted]",
		"ZONE=b context:top false [redacted]",
		"app.toml=a2V5 context:top true [redacted]",
	}; !reflect.DeepEqual(elements, want) {
		t.Errorf("elements %q, want %q", elements, want)
	}
	if want := map[string][]string{
		"before_plan": {"top", "team", "auto", "stack"},
		"after_plan":  {"stack", "auto", "team", "top"},
	}; !reflect.DeepEqual(got.Hooks, want) {
		t.Errorf("hooks %v, want %v", got.Hooks, want)
	}
	if _, err := f.svc.Resolve(
		ctx,
		f.acct,
		kernel.NewULID(),
	); !errors.Is(
		err,
		configapi.ErrNoStack,
	) {
		t.Errorf("a missing stack = %v", err)
	}
}

// TestAccTemplates checks a value's templates when it is saved: against
// the variables its stack resolves to, so a misspelt one is refused with
// its range, or against any name in a context's; a value reading a
// write-only variable becomes write-only, and a value is stored
// unrendered.
func TestAccTemplates(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	set := func(name, v string) (core.Element, error) {
		e, _, err := f.svc.SetStackElement(ctx, f.admin, "web", variable(name, v), "", "/configId")
		return e, err
	}
	f.create(t, "Shared", "team", variable("ZONE", "a"))
	if _, _, err := f.svc.Attach(ctx, f.admin, "shared", "web", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.SetStackElement(
		ctx,
		f.admin,
		"web",
		core.ElementInput{
			Name:      "TOKEN",
			Value:     new("s3cret"),
			WriteOnly: true,
		},
		"",
		"/configId",
	); err != nil {
		t.Fatal(err)
	}
	e, err := set("URL", "https://${{ env.ZONE }}.${{ stack.id }}/$NAME/${NAME}")
	if err != nil || e.WriteOnly ||
		*e.Value != "https://${{ env.ZONE }}.${{ stack.id }}/$NAME/${NAME}" {
		t.Errorf("a template = %+v, %v", e, err)
	}
	if e, err := set(
		"AUTH",
		`Bearer ${{ env["TOKEN"] }}`,
	); err != nil || !e.WriteOnly ||
		e.Value != nil {
		t.Errorf("a template reading a write-only variable = %+v, %v", e, err)
	}
	if _, err := set("my-var", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := set("DASHED", `${{ env["my-var"] }}`); err != nil {
		t.Errorf("a name that is no identifier = %v", err)
	}
	for _, c := range []struct{ name, value string }{
		{"BAD", "${{ env.TYPO }}"},
		{"BAD", `${{ env["my_var"] }}`},
		{"SELF", "${{ env.SELF }}"},
		{"BAD", "${{ stack.nmae }}"},
		{"BAD", "${{ env.ZONE"},
	} {
		_, err := set(c.name, c.value)
		inv, ok := errors.AsType[*kernel.Invalid](err)
		if !ok || len(inv.Errors) != 1 || inv.Errors[0].Pointer != "/value" ||
			inv.Errors[0].Range == nil {
			t.Errorf("%s = %v", c.value, err)
		}
	}
	if _, err := f.svc.StackElement(ctx, f.acct, "web", "BAD"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("a refused element was stored: %v", err)
	}
	if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", core.ElementInput{
		Name: "f.txt", FileMount: true, Value: new("JHt7IGVudi5UWVBPIH19"),
	}, "", "/configId"); err != nil {
		t.Errorf("a mounted file's content is no template: %v", err)
	}

	f.create(t, "Own", "team",
		core.ElementInput{Name: "SECRET", Value: new("x"), WriteOnly: true}, variable("PLAIN", "y"))
	for _, c := range []struct {
		value     string
		writeOnly bool
	}{
		{"${{ env.ANY_NAME }}", false},
		{"${{ env.PLAIN }}", false},
		{"${{ env.SECRET }}", true},
	} {
		e, _, err := f.svc.SetElement(ctx, f.admin, "own", variable("V", c.value), "", "/configId")
		if err != nil || e.WriteOnly != c.writeOnly {
			t.Errorf("%s in a context = %+v, %v", c.value, e, err)
		}
	}
	if _, _, err := f.svc.SetElement(ctx, f.admin, "own", variable("V", "${{ stack.nmae }}"),
		"", "/configId"); !reflect.DeepEqual(pointers(t, err), []string{"/value"}) {
		t.Errorf("a misspelt stack field in a context = %v", err)
	}
}

// TestAccSurface describes a stack as the environment surface's context to
// a caller who reads its space, and checks a source against it.
func TestAccSurface(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t)
	f.stacks.labels["web"] = []string{"prod"}
	for _, e := range []core.ElementInput{
		{Name: "TOKEN", Value: new("s3cret"), WriteOnly: true},
		variable("REGION", "eu"),
		{Name: "app.toml", FileMount: true, Value: new("a2V5")},
	} {
		if _, _, err := f.svc.SetStackElement(ctx, f.admin, "web", e, "", "/configId"); err != nil {
			t.Fatal(err)
		}
	}
	s := f.svc.Surface()
	got, err := s.Env(ctx, f.reader, "web")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{
		"env": map[string]any{"REGION": "eu", "TOKEN": ""},
		"stack": map[string]any{
			"id": "web", "name": "Web", "space": "app", "labels": []string{"prod"},
			"repository": "web", "branch": "main", "projectRoot": "",
		},
	}; !reflect.DeepEqual(got.Sample, want) {
		t.Errorf("sample %v, want %v", got.Sample, want)
	}
	r, err := expr.NewRegistry(expr.Defaults(), s)
	if err != nil {
		t.Fatal(err)
	}
	for src, ok := range map[string]bool{
		"${{ env.REGION + env.TOKEN }}": true, "${{ stack.nmae }}": false, "${{ stack.labels[0] }}": true,
	} {
		chk, err := r.Check(expr.Request{
			Surface: s.Name, Variables: got.Variables, Form: expr.FormTemplate, Source: src,
		})
		if err != nil || chk.OK() != ok {
			t.Errorf("%s = %+v, %v", src, chk, err)
		}
	}
	if _, err := s.Env(ctx, f.reader, "db"); !missing(err, "stack") {
		t.Errorf("a stack in a space the reader cannot read = %v", err)
	}
	if _, err := s.Env(ctx, f.admin, "gone"); !missing(err, "stack") {
		t.Errorf("a missing stack = %v", err)
	}
	f.spaces.err = errors.New("down")
	if _, err := s.Env(ctx, f.admin, "web"); err == nil {
		t.Error("the spaces down answered")
	}
	f.spaces.err = nil
	f.stacks.err = errors.New("down")
	if _, err := s.Env(ctx, f.admin, "web"); err == nil {
		t.Error("the stacks down answered")
	}
}
