package http

import (
	"context"
	"errors"
	"math"
	"time"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/apisearch"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/ignored"
)

// StackSearchRunIgnoredTriggers is served through VCSAPI. Shared controls have
// checked admin reach and ignored-runs; recording itself is never tier-gated.
func (s Server) StackSearchRunIgnoredTriggers(ctx context.Context, req api.StackSearchRunIgnoredTriggersRequestObject) (api.StackSearchRunIgnoredTriggersResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if s.ignored == nil {
		return nil, kernel.ErrUnavailable
	}
	rows, err := s.ignored.Search(ctx, p, req.StackId, apisearch.Input(req.Params.Input))
	if errors.Is(err, stacksapi.ErrNotFound) {
		return nil, problem.NotFound.Error("No such stack in this account.")
	} //nolint:wrapcheck // public problem identity is intentional
	if errors.Is(err, ignored.ErrForbidden) {
		p := problem.Forbidden.New("Ignored triggers require admin access to the stack's space.")
		p.Action = string(accessapi.ActionSpaceAdmin)
		return nil, &problem.Error{Problem: p}
	}
	if err != nil {
		return nil, err
	} //nolint:wrapcheck // the search names its errors and preserves cursor problems
	out := api.SearchRunIgnoredTriggersOutput{Edges: make([]api.RunIgnoredTriggersConnection, 0, len(rows.Edges)), PageInfo: apisearch.PageInfo(rows)}
	for _, edge := range rows.Edges {
		r := edge.Node
		c := r.Commit
		out.Edges = append(
			out.Edges,
			api.RunIgnoredTriggersConnection{
				Cursor: edge.Cursor,
				Node: api.RunIgnoredTrigger{
					Id:                r.ID.String(),
					CreatedAt:         ignoredUnix(r.CreatedAt),
					ReasonType:        api.RunIgnoredTriggerReasonType(r.Reason),
					PolicyAttachments: []api.RunIgnoredTriggerPolicyAttachments{},
					Commit: api.Commit{
						Hash:        c.Hash,
						Tag:         c.Tag,
						Url:         c.URL,
						Message:     c.Message,
						AuthorName:  c.AuthorName,
						AuthorLogin: c.AuthorLogin,
						Timestamp:   ignoredUnix(c.Timestamp),
					},
				},
			},
		)
	}
	return api.StackSearchRunIgnoredTriggers200JSONResponse{Body: out}, nil
}

func ignoredUnix(value time.Time) int32 {
	return int32(max(math.MinInt32, min(math.MaxInt32, value.Unix())))
} //nolint:gosec // clamped to the served timestamp range
