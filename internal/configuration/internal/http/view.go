package http

import (
	"context"
	"slices"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/configuration/internal/core"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/strict"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

func (s Server) view(ctx context.Context, p kernel.Principal, c core.Context) (api.Context, error) {
	vs, err := s.views(ctx, p, c)
	if err != nil {
		return api.Context{}, err
	}
	return vs[0], nil
}

// views are all as p sees them: the actions p holds in each one's space,
// its space by slug, its config elements with write-only values left out,
// and its attachments with their stacks, read for all at once.
func (s Server) views(
	ctx context.Context,
	p kernel.Principal,
	all ...core.Context,
) ([]api.Context, error) {
	out := make([]api.Context, 0, len(all))
	if len(all) == 0 {
		return out, nil
	}
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return nil, err //nolint:wrapcheck // the access domain names the read
	}
	d, err := s.svc.Details(ctx, p.AccountID, all...)
	if err != nil {
		return nil, err //nolint:wrapcheck // the stores name the reads
	}
	held := map[kernel.ULID][]accessapi.Action{}
	for _, c := range all {
		if _, ok := held[c.Space]; !ok {
			if held[c.Space], err = s.authz.Actions(ctx, p, c.Space); err != nil {
				return nil, err //nolint:wrapcheck // the snapshot's read names itself
			}
		}
	}
	for _, c := range all {
		v := api.Context{
			Id: c.Slug, Name: c.Name, Description: c.Description, Kind: api.ContextKindContext,
			Space: slugs[c.Space], SpaceDetails: api.SpaceRef{Id: slugs[c.Space]},
			Labels: orEmpty(c.Labels), Hooks: hooksOf(c.Hooks),
			AllowedActions: actions(held[c.Space]),
			CreatedAt:      unix(c.CreatedAt.Unix()), UpdatedAt: unix(c.UpdatedAt.Unix()),
			Config:         make([]api.ConfigElement, 0, len(d.Elements[c.ID])),
			AttachedStacks: make([]api.ContextStackAttachment, 0, len(d.Attachments[c.ID])),
		}
		for _, e := range d.Elements[c.ID] {
			v.Config = append(v.Config, elementView(e))
		}
		for _, a := range d.Attachments[c.ID] {
			st := d.Stacks[a.Stack]
			v.AttachedStacks = append(v.AttachedStacks, attachmentView(a, st, slugs[st.Space]))
		}
		v.HasAttachedStacks = len(v.AttachedStacks) > 0
		out = append(out, v)
	}
	return out, nil
}

// attachment is a as the API answers it, with its stack's space by slug.
func (s Server) attachment(
	ctx context.Context,
	p kernel.Principal,
	a core.Attachment,
	st stacksapi.Stack,
) (api.ContextStackAttachment, error) {
	slugs, err := s.spaces.Slugs(ctx, p.AccountID)
	if err != nil {
		return api.ContextStackAttachment{}, err //nolint:wrapcheck // the access domain names the read
	}
	return attachmentView(a, st, slugs[st.Space]), nil
}

func attachmentView(
	a core.Attachment,
	st stacksapi.Stack,
	space string,
) api.ContextStackAttachment {
	return api.ContextStackAttachment{
		Id: a.ID.String(), StackId: st.Slug, StackName: st.Name,
		StackSpaceDetails: api.SpaceRef{Id: space}, Priority: a.Priority,
		IsAutoattached: a.Autoattached, CreatedAt: unix(a.CreatedAt.Unix()),
	}
}

// elementView is e as the API answers it; a write-only value is never
// read, so it is never answered.
func elementView(e core.Element) api.ConfigElement {
	v := api.ConfigElement{
		Id: e.Name, Type: api.ENVIRONMENTVARIABLE, Checksum: e.Checksum, Description: e.Description,
		FileMode: e.FileMode, WriteOnly: e.WriteOnly, CreatedAt: new(unix(e.CreatedAt.Unix())),
	}
	if e.FileMount {
		v.Type = api.FILEMOUNT
	}
	if !e.WriteOnly {
		v.Value = e.Value
	}
	return v
}

// inputOf is in as the service takes it.
func inputOf(in api.ContextInput) core.Input {
	out := core.Input{Name: in.Name, Description: in.Description, Hooks: map[string][]string{}}
	if in.Space != nil {
		out.Space = *in.Space
	}
	if in.Labels != nil {
		out.Labels = *in.Labels
	}
	if h := in.Hooks; h != nil {
		out.Hooks = map[string][]string{
			stacksapi.BeforeInit: h.BeforeInit, stacksapi.AfterInit: h.AfterInit,
			stacksapi.BeforePlan: h.BeforePlan, stacksapi.AfterPlan: h.AfterPlan,
			stacksapi.BeforeApply: h.BeforeApply, stacksapi.AfterApply: h.AfterApply,
			stacksapi.BeforeDestroy: h.BeforeDestroy, stacksapi.AfterDestroy: h.AfterDestroy,
			stacksapi.BeforePerform: h.BeforePerform, stacksapi.AfterPerform: h.AfterPerform,
			stacksapi.AfterRun: h.AfterRun,
		}
	}
	if in.ConfigAttachments != nil {
		out.Config = make([]core.ElementInput, 0, len(*in.ConfigAttachments))
		for _, e := range *in.ConfigAttachments {
			out.Config = append(out.Config, core.ElementInput{
				Name:        e.Id,
				FileMount:   e.Type == api.FILEMOUNT,
				Value:       &e.Value,
				WriteOnly:   e.WriteOnly,
				Description: e.Description,
				FileMode:    e.FileMode,
			})
		}
	}
	if in.StackAttachments != nil {
		for _, a := range *in.StackAttachments {
			out.Stacks = append(out.Stacks, core.StackInput{Stack: a.Stack, Priority: a.Priority})
		}
	}
	return out
}

// inputView is c's fields as the ContextInput a PUT would send to keep
// them, with its space by slug and no config elements: the document a
// merge patch applies to.
func inputView(c core.Context, space string) api.ContextInput {
	h := api.HooksInput(hooksOf(c.Hooks))
	labels := slices.Clone(orEmpty(c.Labels))
	return api.ContextInput{
		Name: c.Name, Description: c.Description, Space: &space, Labels: &labels, Hooks: &h,
	}
}

// hooksOf is each phase's commands, an empty list for a phase with none.
func hooksOf(h map[string][]string) api.Hooks {
	at := func(phase string) []string { return orEmpty(h[phase]) }
	return api.Hooks{
		BeforeInit: at(stacksapi.BeforeInit), AfterInit: at(stacksapi.AfterInit),
		BeforePlan: at(stacksapi.BeforePlan), AfterPlan: at(stacksapi.AfterPlan),
		BeforeApply: at(stacksapi.BeforeApply), AfterApply: at(stacksapi.AfterApply),
		BeforeDestroy: at(stacksapi.BeforeDestroy), AfterDestroy: at(stacksapi.AfterDestroy),
		BeforePerform: at(stacksapi.BeforePerform), AfterPerform: at(stacksapi.AfterPerform),
		AfterRun: at(stacksapi.AfterRun),
	}
}

func actions(as []accessapi.Action) []api.Action {
	out := make([]api.Action, 0, len(as))
	for _, a := range as {
		out = append(out, api.Action(a))
	}
	return out
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// etag is c's entity tag, which an If-Match names to change it.
func etag(c core.Context) *string {
	t := strict.ETag(c.Version)
	return &t
}

// unix is seconds as the API's int32 timestamps carry them, which hold
// every time before 2038.
func unix(s int64) int32 { return int32(s) } //nolint:gosec // as above
