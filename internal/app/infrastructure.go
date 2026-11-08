package app

import (
	"context"
	"math"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

// QueueStats reads each logical queue's depth; *queue.Queue is one.
type QueueStats interface {
	Stats(ctx context.Context) ([]queue.Stat, error)
}

// queueStatsList names messageQueueStats' cursors, so one from another list
// is refused.
const queueStatsList = "message-queue-stats"

// MessageQueueStats reports every logical queue of the persistent work
// queue, in its table order, with the jobs ready to claim as visible and
// the running ones as in flight. The counts are installation-wide. The list
// is short and fixed, so a cursor holds the position of a page's last queue.
//
// ref: decision 0011
func (s apiServer) MessageQueueStats(
	ctx context.Context,
	req api.MessageQueueStatsRequestObject,
) (api.MessageQueueStatsResponseObject, error) {
	from := 0
	if req.Params.Cursor != nil {
		c, err := page.Decode[int](queueStatsList, *req.Params.Cursor)
		if err != nil {
			return nil, err //nolint:wrapcheck // a problem, answered as it is
		}
		from = c.Key + 1
	}
	stats, err := s.queues.Stats(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store marks an unreachable database
	}
	from = min(max(from, 0), len(stats))
	to := min(from+int(kernel.PageLimit(req.Params.Limit)), len(stats))
	body := api.MessageQueueStatsPage{Items: make([]api.MessageQueueStats, 0, to-from)}
	for _, st := range stats[from:to] {
		body.Items = append(body.Items, api.MessageQueueStats{
			ClientType: "postgres",
			QueueName:  string(st.Queue),
			Visible:    count(st.Visible),
			InFlight:   count(st.InFlight),
		})
	}
	if to < len(stats) {
		next, err := page.Encode(queueStatsList,
			kernel.Cursor[int]{Key: to - 1, ID: string(stats[to-1].Queue)})
		if err != nil {
			return nil, err //nolint:wrapcheck // encoding an int cannot fail
		}
		body.NextCursor = &next
	}
	return api.MessageQueueStats200JSONResponse{Body: body}, nil
}

// count fits a job count into the reference's int32, saturating rather
// than wrapping.
func count(n int) int32 {
	return int32(min(n, math.MaxInt32)) //nolint:gosec // clamped above
}
