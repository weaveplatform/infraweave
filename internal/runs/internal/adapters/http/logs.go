package http

import (
	"context"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
)

// logsList is the list runLogs' tokens belong to.
const logsList = "run-logs"

// position is a runLogs token's place in a state's log: the state, its
// state version and the chunk the next page reads from. The cursor's ID
// is the run as the request named it.
type position struct {
	State   kernel.RunState `json:"s"`
	Version int64           `json:"v"`
	Seq     int64           `json:"q"`
}

// RunLogs answers a page of a state's log of one run of the caller's
// account. The route has decided SPACE_READ on its stack's space.
func (s Server) RunLogs(
	ctx context.Context,
	req api.RunLogsRequestObject,
) (api.RunLogsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	q := logs.Query{
		State: kernel.RunState(req.Params.State), Sanitize: deref(req.Params.Sanitize),
	}
	if v := req.Params.StateVersion; v != nil {
		version := int64(*v)
		q.Version = &version
	}
	if t := req.Params.Token; t != nil {
		c, err := page.Decode[position](logsList, *t)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		switch {
		case !strings.EqualFold(c.ID, req.RunId) || c.Key.State != q.State:
			//nolint:wrapcheck // as above
			return nil, problem.InvalidCursor.Error("The token belongs to another run's or " +
				"state's log.")
		case q.Version != nil && *q.Version != c.Key.Version:
			//nolint:wrapcheck // as above
			return nil, problem.InvalidCursor.Error("The token belongs to another state version.")
		}
		q.Version, q.From = &c.Key.Version, c.Key.Seq
	}
	pg, err := s.logs.Page(ctx, p, req.RunId, q)
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	messages := make([]api.LogMessage, len(pg.Lines))
	for i, l := range pg.Lines {
		messages[i] = api.LogMessage{Message: l.Message, Timestamp: unix(l.At.Unix())}
	}
	body := api.LogsResponse{
		Exists: pg.Exists, Expired: pg.Expired, Finished: pg.Finished(), HasMore: pg.HasMore,
		Messages: messages, Ongoing: pg.Ongoing,
	}
	if pg.More() {
		next, err := page.Encode(logsList, kernel.Cursor[position]{
			Key: position{State: q.State, Version: pg.Version, Seq: pg.Next}, ID: req.RunId,
		})
		if err != nil {
			return nil, err //nolint:wrapcheck // names the cursor
		}
		body.NextToken = &next
	}
	return api.RunLogs200JSONResponse{Body: body}, nil
}

// RunLogsDownloadURL answers where a state's whole log of one run of the
// caller's account downloads from. The route has decided SPACE_READ on
// its stack's space.
func (s Server) RunLogsDownloadURL(
	ctx context.Context,
	req api.RunLogsDownloadURLRequestObject,
) (api.RunLogsDownloadURLResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	var version *int64
	if v := req.Params.StateVersion; v != nil {
		n := int64(*v)
		version = &n
	}
	d, err := s.logs.Download(ctx, p, req.RunId, kernel.RunState(req.Params.State), version)
	if err != nil {
		return nil, runError(err, "", req.RunId)
	}
	body := api.LogsDownloadURL{Expired: d.Expired, Ongoing: d.Ongoing}
	if d.URL != "" {
		body.Url = &d.URL
	}
	return api.RunLogsDownloadURL200JSONResponse{Body: body}, nil
}
