package http

import (
	"context"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// unbound is the configuration of a server whose configuration domain is
// not bound: no stack has a context or a config element.
type unbound struct{}

func (unbound) AttachedContexts(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID][]stacksapi.AttachedContext, error) {
	return map[kernel.ULID][]stacksapi.AttachedContext{}, nil
}

func (unbound) Config(
	context.Context,
	kernel.AccountID,
	[]kernel.ULID,
) (map[kernel.ULID][]stacksapi.ConfigElement, error) {
	return map[kernel.ULID][]stacksapi.ConfigElement{}, nil
}

// edges holds the server's back edges. Every copy of a Server shares
// one, so a binding made after the server was handed out reaches it.
type edges struct {
	config stacksapi.StackConfiguration
}

// BindConfiguration makes c the configuration every copy of s reads. It
// runs during construction, before the server serves a request.
func (s Server) BindConfiguration(c stacksapi.StackConfiguration) { s.edges.config = c }

// contextView is a context attachment as the API answers it.
func contextView(c stacksapi.AttachedContext) api.StackContextAttachment {
	return api.StackContextAttachment{
		Id:                 c.ID.String(),
		ContextId:          c.Context,
		ContextName:        c.Name,
		ContextDescription: c.Description,
		ContextLabels:      orEmpty(c.Labels),
		ContextHooks:       hooksOf(c.Hooks),
		Config:             elementViews(c.Config),
		Priority:           c.Priority,
		IsAutoattached:     c.Autoattached,
	}
}

// elementViews are config elements as the API answers them.
func elementViews(es []stacksapi.ConfigElement) []api.ConfigElement {
	out := make([]api.ConfigElement, 0, len(es))
	for _, e := range es {
		v := api.ConfigElement{
			Id:          e.ID,
			Type:        api.ENVIRONMENTVARIABLE,
			Checksum:    e.Checksum,
			Description: e.Description,
			FileMode:    e.FileMode,
			WriteOnly:   e.WriteOnly,
		}
		if e.FileMount {
			v.Type = api.FILEMOUNT
		}
		if !e.WriteOnly {
			v.Value = e.Value
		}
		if !e.CreatedAt.IsZero() {
			v.CreatedAt = new(unix(e.CreatedAt.Unix()))
		}
		out = append(out, v)
	}
	return out
}
