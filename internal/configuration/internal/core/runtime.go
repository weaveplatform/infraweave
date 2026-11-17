package core

import (
	"context"
	"slices"
	"strings"

	configapi "github.com/weaveplatform/infraweave/internal/configuration/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// RuntimeConfig reads each stack's stored configuration winners, followed
// by computed placeholders for names no stored source sets. It reads all
// stacks' sources in one batch and never opens a write-only value. The
// actual API token and phase state are injected separately at execution.
//
// ref: decision 0008 § "The run specification and its secrets"
func (s *Service) RuntimeConfig(ctx context.Context, acct kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.ConfigElementWithSource, error) {
	out := map[kernel.ULID][]stacksapi.ConfigElementWithSource{}
	if len(stacks) == 0 {
		return out, nil
	}
	attached, err := s.store.Attachments(ctx, acct, stacks, true)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	owners := make([]Owner, 0, len(stacks))
	for _, id := range stacks {
		owners = append(owners, Owner{Stack: id})
	}
	seen := map[kernel.ULID]bool{}
	var ids []kernel.ULID
	for _, as := range attached {
		for _, a := range as {
			if !seen[a.Context] {
				seen[a.Context] = true
				ids = append(ids, a.Context)
				owners = append(owners, Owner{Context: a.Context})
			}
		}
	}
	contexts, err := s.store.ByIDs(ctx, acct, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	elements, err := s.readElements(ctx, acct, owners)
	if err != nil {
		return nil, err
	}
	for _, id := range stacks {
		var sources []source
		attachments := map[kernel.ULID]*stacksapi.AttachedContext{}
		for _, a := range attached[id] {
			c := contexts[a.Context]
			kind := configapi.SourceContext
			if a.Autoattached {
				kind = configapi.SourceAutoattached
			}
			sources = append(sources, source{
				kind: kind, context: c.Slug, name: c.Name, priority: a.Priority,
				elements: elements[c.ID],
			})
			attachments[c.ID] = &stacksapi.AttachedContext{
				ID: a.ID, Context: c.Slug, Name: c.Name, Description: c.Description,
				Labels: c.Labels, Hooks: c.Hooks, Config: ConfigOf(openedElements(elements[c.ID])),
				Priority: a.Priority, Autoattached: a.Autoattached,
			}
		}
		all := append([]source{{kind: configapi.SourceStack, elements: elements[id]}}, ranked(sources)...)
		out[id] = runtimeConfig(winners(all), attachments)
	}
	return out, nil
}

// openedElements discards the wrapper of elements read without opening
// write-only values, so the normal config view supplies their metadata.
func openedElements(es []Opened) []Element {
	out := make([]Element, len(es))
	for i, e := range es {
		out[i] = e.Element
	}
	return out
}

func runtimeConfig(es []Opened, contexts map[kernel.ULID]*stacksapi.AttachedContext) []stacksapi.ConfigElementWithSource {
	config := ConfigOf(openedElements(es))
	out := make([]stacksapi.ConfigElementWithSource, 0, len(config)+len(kernel.ComputedEnvironmentNames()))
	seen := map[string]bool{}
	for i, e := range es {
		seen[e.Name] = true
		out = append(out, stacksapi.ConfigElementWithSource{
			ConfigElement: config[i], Context: contexts[e.Owner.Context],
		})
	}
	for _, name := range kernel.ComputedEnvironmentNames() {
		if !seen[name] {
			out = append(out, stacksapi.ConfigElementWithSource{
				ConfigElement: stacksapi.ConfigElement{ID: name, WriteOnly: name == kernel.APITokenVariable},
				Runtime:       true,
			})
		}
	}
	slices.SortFunc(out, func(a, b stacksapi.ConfigElementWithSource) int { return strings.Compare(a.ID, b.ID) })
	return out
}
