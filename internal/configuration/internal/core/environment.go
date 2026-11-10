package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/expr"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Opened is an element with its value opened, write-only or not, as a
// run's spec reads it.
type Opened struct {
	Element
	Secret configapi.Secret
}

// errNoChecker is a template saved before internal/app binds a Checker.
var errNoChecker = errors.New("configuration: no expression checker is bound")

// Checker type-checks a source against a surface; expr.Registry is one.
type Checker interface {
	Check(req expr.Request) (expr.Check, error)
}

// BindExpressions binds the checker a value's templates are checked with
// when it is saved, before the service takes a request: the registry is
// built from the service's own surface, so it cannot be one of Deps.
func (s *Service) BindExpressions(c Checker) { s.exprs = c }

// The environment surface's variables. env is a map, so a name that is
// no identifier is read as env["NAME"]; a value saved on a stack may read
// only the variables the stack resolves to, and one saved on a context,
// which many stacks attach, any.
const (
	envVariable   = "env"
	stackVariable = "stack"
)

var (
	envSchema   = json.RawMessage(`{"type": "object", "additionalProperties": {"type": "string"}}`)
	stackSchema = json.RawMessage(`{"type": "object", "properties": {
		"id": {"type": "string"}, "name": {"type": "string"}, "space": {"type": "string"},
		"labels": {"type": "array", "items": {"type": "string"}},
		"repository": {"type": "string"}, "branch": {"type": "string"},
		"projectRoot": {"type": "string"}
	}, "additionalProperties": false}`)
)

// Surface is the environment surface, configapi.Surface, with env and
// stack.
//
// ref: decision 0024 "Surfaces and their variables"
func (s *Service) Surface() expr.Surface {
	return expr.Surface{
		Name: configapi.Surface,
		Variables: []expr.Variable{
			{Name: envVariable, Schema: envSchema},
			{Name: stackVariable, Schema: stackSchema},
		},
		Env: s.surfaceContext,
	}
}

// surfaceContext describes the stack ref as the surface's context, for
// p, who must read its space: sample values of the variables it resolves
// to, a write-only variable's masked as a run's log masks it, and of the
// stack, env closed to the names it resolves to. A stack p cannot read is expr.ErrNoContext, as a missing one is.
func (s *Service) surfaceContext(
	ctx context.Context,
	p kernel.Principal,
	ref string,
) (expr.Context, error) {
	st, err := s.stack(ctx, p.AccountID, ref)
	if _, ok := errors.AsType[*Missing](err); ok {
		return expr.Context{}, fmt.Errorf("%w: %w", expr.ErrNoContext, err)
	}
	if err != nil {
		return expr.Context{}, err
	}
	read, err := s.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, st.Space)
	switch {
	case err != nil:
		return expr.Context{}, err //nolint:wrapcheck // the snapshot's read names itself
	case !read:
		return expr.Context{}, fmt.Errorf("%w: %w", expr.ErrNoContext,
			&Missing{Kind: "stack", ID: ref})
	}
	sources, settings, err := s.sources(ctx, p.AccountID, st.ID, false)
	if err != nil {
		return expr.Context{}, err
	}
	env := map[string]any{}
	for _, e := range winners(sources) {
		if e.FileMount {
			continue
		}
		env[e.Name] = mask.Replacement
		if !e.WriteOnly && e.Value != nil {
			env[e.Name] = *e.Value
		}
	}
	labels, err := s.stacks.Labels(ctx, p.AccountID, []kernel.ULID{st.ID})
	if err != nil {
		return expr.Context{}, err //nolint:wrapcheck // the stacks domain names the read
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return expr.Context{}, err //nolint:wrapcheck // the access domain names the read
	}
	stack := map[string]any{
		"id": st.Slug, "name": st.Name, "space": slugs[st.Space],
		"labels": slices.Clone(labels[st.ID]), "repository": settings.Repository,
		"branch": settings.Branch, "projectRoot": "",
	}
	if settings.ProjectRoot != nil {
		stack["projectRoot"] = *settings.ProjectRoot
	}
	return expr.Context{
		Keys:   map[string][]string{envVariable: slices.Sorted(maps.Keys(env))},
		Sample: map[string]any{envVariable: env, stackVariable: stack},
	}, nil
}

// source is one place a stack's environment comes from: the stack, or a
// context attached to it, by hand at a priority or automatically.
type source struct {
	kind     string
	context  string
	name     string
	priority int32
	elements []Opened
	hooks    map[string][]string
}

// ranked are attached in rank order, highest first: those attached by
// hand by priority, lowest first, then by slug; then those attached
// automatically, by name.
//
// ref: report 13 §7.1
func ranked(attached []source) []source {
	out := slices.Clone(attached)
	slices.SortStableFunc(out, func(a, b source) int {
		auto := a.kind == configapi.SourceAutoattached
		switch {
		case auto != (b.kind == configapi.SourceAutoattached) && auto:
			return 1
		case auto != (b.kind == configapi.SourceAutoattached):
			return -1
		case auto:
			return cmp.Or(strings.Compare(a.name, b.name), strings.Compare(a.context, b.context))
		}
		return cmp.Or(cmp.Compare(a.priority, b.priority), strings.Compare(a.context, b.context))
	})
	return out
}

// winners are the elements of stack and attached that win, by name: the
// stack's own, then each context's in rank order, the first of a name
// winning.
func winners(all []source) []Opened {
	seen := map[string]bool{}
	var out []Opened
	for _, src := range all {
		for _, e := range src.elements {
			if !seen[e.Name] {
				seen[e.Name] = true
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, func(a, b Opened) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// resolve is the environment of stack with attached: its elements, each
// from the first source of its name in rank order, and each phase's
// hooks. Before a phase, the contexts attached by hand run first, by
// priority, then those attached automatically, last name first, then the
// stack; after a phase the reverse, so the outermost wraps the phase.
//
// ref: report 13 §5.5, §7.1
func resolve(stack source, attached []source) configapi.Resolved {
	rank := append([]source{stack}, ranked(attached)...)
	out := configapi.Resolved{Elements: []configapi.Element{}, Hooks: map[string][]string{}}
	from := map[string]source{}
	for _, src := range rank {
		for _, e := range src.elements {
			if _, ok := from[e.Name]; !ok {
				from[e.Name] = src
			}
		}
	}
	for _, e := range winners(rank) {
		src := from[e.Name]
		out.Elements = append(out.Elements, configapi.Element{
			Name: e.Name, FileMount: e.FileMount, WriteOnly: e.WriteOnly, FileMode: e.FileMode,
			Checksum: e.Checksum, Value: e.Secret, Source: src.kind, Context: src.context,
		})
	}
	var before []source
	for _, src := range rank[1:] {
		if src.kind != configapi.SourceAutoattached {
			before = append(before, src)
		}
	}
	for _, src := range slices.Backward(rank[1:]) {
		if src.kind == configapi.SourceAutoattached {
			before = append(before, src)
		}
	}
	before = append(before, stack)
	after := slices.Clone(before)
	slices.Reverse(after)
	for _, src := range rank {
		for phase := range src.hooks {
			if _, ok := out.Hooks[phase]; ok {
				continue
			}
			order := after
			if strings.HasPrefix(phase, "before_") {
				order = before
			}
			var cmds []string
			for _, o := range order {
				cmds = append(cmds, o.hooks[phase]...)
			}
			if len(cmds) > 0 {
				out.Hooks[phase] = cmds
			}
		}
	}
	return out
}

// Resolve resolves acct's stack id's environment for a run's spec,
// opening every value: the one place a write-only value is opened.
//
// ref: decision 0013 "Delivery to runs"
func (s *Service) Resolve(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (configapi.Resolved, error) {
	sources, _, err := s.sources(ctx, acct, id, true)
	if err != nil {
		return configapi.Resolved{}, err
	}
	return resolve(sources[0], sources[1:]), nil
}

// sources are acct's stack id's sources, the stack's first, with its
// settings; with open, each element's value opened, write-only or not,
// and otherwise only a readable one, as Value.
func (s *Service) sources(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	open bool,
) ([]source, stacksapi.Settings, error) {
	settings, err := s.stacks.Settings(ctx, acct, id)
	if errors.Is(err, stacksapi.ErrNotFound) {
		return nil, settings, fmt.Errorf("%w: %s", configapi.ErrNoStack, id)
	} else if err != nil {
		return nil, settings, err //nolint:wrapcheck // the stacks domain names the read
	}
	attached, err := s.store.Attachments(ctx, acct, []kernel.ULID{id}, true)
	if err != nil {
		return nil, settings, err //nolint:wrapcheck // the store names the read
	}
	owners := []Owner{{Stack: id}}
	ids := make([]kernel.ULID, 0, len(attached[id]))
	for _, a := range attached[id] {
		owners = append(owners, Owner{Context: a.Context})
		ids = append(ids, a.Context)
	}
	contexts, err := s.store.ByIDs(ctx, acct, ids)
	if err != nil {
		return nil, settings, err //nolint:wrapcheck // the store names the read
	}
	var elements map[kernel.ULID][]Opened
	if open {
		elements, err = s.store.Opened(ctx, acct, owners)
	} else {
		elements, err = s.readElements(ctx, acct, owners)
	}
	if err != nil {
		return nil, settings, err
	}
	out := []source{{
		kind: configapi.SourceStack, elements: elements[id], hooks: settings.Hooks,
	}}
	for _, a := range attached[id] {
		c := contexts[a.Context]
		kind := configapi.SourceContext
		if a.Autoattached {
			kind = configapi.SourceAutoattached
		}
		out = append(out, source{
			kind: kind, context: c.Slug, name: c.Name, priority: a.Priority,
			elements: elements[c.ID], hooks: c.Hooks,
		})
	}
	return out, settings, nil
}

// readElements are owners' elements as a read answers them, a write-only
// value left out.
func (s *Service) readElements(
	ctx context.Context,
	acct kernel.AccountID,
	owners []Owner,
) (map[kernel.ULID][]Opened, error) {
	all, err := s.store.Elements(ctx, acct, owners)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	out := make(map[kernel.ULID][]Opened, len(all))
	for id, es := range all {
		for _, e := range es {
			out[id] = append(out[id], Opened{Element: e})
		}
	}
	return out, nil
}

// template checks the ${{ … }} templates of in's value, an environment
// variable's, against the environment surface as owner sees it, and
// reports whether the value reads a write-only variable, which makes it
// write-only too. A refusal points at /value.
//
// ref: decision 0024 "Type-checking at save"
func (s *Service) template(
	ctx context.Context,
	acct kernel.AccountID,
	owner Owner,
	in ElementInput,
) (bool, error) {
	if in.FileMount || in.Value == nil || !strings.Contains(*in.Value, "${{") {
		return false, nil
	}
	if s.exprs == nil {
		return false, errNoChecker
	}
	// writeOnly holds each variable the value may read, nil admitting any,
	// and whether it is write-only; secret holds those that are.
	var writeOnly map[string]bool
	secret := map[string]bool{}
	if owner.Stack.IsZero() {
		own, err := s.store.Elements(ctx, acct, []Owner{owner})
		if err != nil {
			return false, err //nolint:wrapcheck // the store names the read
		}
		for _, e := range own[owner.Context] {
			if e.WriteOnly && !e.FileMount && e.Name != in.Name {
				secret[e.Name] = true
			}
		}
	} else {
		sources, _, err := s.sources(ctx, acct, owner.Stack, false)
		if err != nil {
			return false, err
		}
		writeOnly = map[string]bool{}
		for _, e := range winners(sources) {
			if !e.FileMount && e.Name != in.Name {
				writeOnly[e.Name] = e.WriteOnly
			}
		}
		secret = writeOnly
	}
	chk, err := s.exprs.Check(expr.Request{
		Surface: configapi.Surface, Form: expr.FormTemplate, Source: *in.Value,
	})
	if err != nil {
		return false, fmt.Errorf("configuration: check a template: %w", err)
	}
	inv := &kernel.Invalid{Detail: "The config element's value does not compile."}
	for _, d := range chk.Diagnostics {
		if d.Severity == expr.SeverityError {
			inv.Errors = append(inv.Errors, d.FieldError("/value"))
		}
	}
	reads := false
	for _, r := range chk.Reads {
		if r.Variable != envVariable {
			continue
		}
		if _, ok := writeOnly[r.Key]; writeOnly != nil && !ok {
			rng := r.Range
			inv.Errors = append(inv.Errors, kernel.FieldError{
				Pointer: "/value", Reason: kernel.ReasonRule, Range: &rng,
				Message: fmt.Sprintf("The stack's environment has no variable %s.", r.Key),
			})
		}
		reads = reads || secret[r.Key]
	}
	if len(inv.Errors) > 0 {
		return false, inv
	}
	return reads, nil
}
