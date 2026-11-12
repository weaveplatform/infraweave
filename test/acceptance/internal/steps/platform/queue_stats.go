package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/pkg/sdk"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// queueStat and queueStatsPage are messageQueueStats' documented JSON.
type queueStat struct {
	ClientType string `json:"clientType"`
	QueueName  string `json:"queueName"`
	Visible    int    `json:"visible"`
	InFlight   int    `json:"inFlight"`
}

type queueStatsPage struct {
	Items      []queueStat `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

// logicalQueues are the work queue's seven logical queues, in the order
// messageQueueStats lists them.
//
// ref: decision 0011
var logicalQueues = []string{
	"default", "events", "vcs-inbox", "deliveries", "scheduler", "flows", "maintenance",
}

// queueStatSteps are the steps of messageQueueStats.
type queueStatSteps struct{ w *harness.World }

func (s *queueStatSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^(\d+) due jobs?, (\d+) running jobs? and (\d+) jobs? due tomorrow on the "([a-z-]+)" queue$`,
		s.jobs,
	)
	sc.Step(`^(\d+) running jobs? on the "([a-z-]+)" queue$`,
		func(ctx context.Context, running int, queue string) error {
			return s.jobs(ctx, 0, running, 0, queue)
		})
	sc.Step(
		`^messageQueueStats lists the seven logical queues in order, each served by "([a-z_]+)"$`,
		s.statsList,
	)
	sc.Step(
		`^messageQueueStats reports (\d+) visible and (\d+) in flight on the "([a-z-]+)" queue$`,
		s.statsReport,
	)
	sc.Step(`^messageQueueStats read (\d+) at a time lists the seven logical queues in order$`,
		s.statsPaged)
}

// jobs inserts jobs through the scenario's own connection, as other
// replicas' enqueues would.
func (s *queueStatSteps) jobs(ctx context.Context, due, running, later int, queue string) error {
	var stmts []string
	insert := func(n int, state, runAt, lease string) {
		for range n {
			stmts = append(stmts, fmt.Sprintf(`INSERT INTO jobs
    (queue, kind, payload, priority, run_at, state, max_attempts, lease_until)
VALUES (%s, 'acceptance.work', '{}', 5, %s, '%s', 5, %s);`, harness.Literal(queue), runAt, state, lease))
		}
	}
	insert(due, "pending", "now()", "NULL")
	insert(running, "running", "now()", "now() + interval '5 minutes'")
	insert(later, "pending", "now() + interval '1 day'", "NULL")
	_, err := s.w.Query(ctx, strings.Join(stmts, "\n"))
	return err
}

// statsPage reads one page of messageQueueStats through the SDK; a limit
// of 0 sends none.
func (s *queueStatSteps) statsPage(
	ctx context.Context,
	limit int,
	cursor *string,
) ([]queueStat, *string, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != nil {
		q.Set("cursor", *cursor)
	}
	resp, err := s.w.APIs[0].Do(ctx, http.MethodGet, "/api/v1/message-queue-stats?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("messageQueueStats: %w", err)
	}
	var p queueStatsPage
	if err := json.Unmarshal(resp.Body, &p); err != nil {
		return nil, nil, fmt.Errorf("%w: messageQueueStats answered %s", harness.ErrStep, resp.Body)
	}
	return p.Items, p.NextCursor, nil
}

func (s *queueStatSteps) stats(ctx context.Context) ([]queueStat, error) {
	var all []queueStat
	for it, err := range sdk.Iterate(ctx, func(ctx context.Context, c *string) ([]queueStat, *string, error) {
		return s.statsPage(ctx, 0, c)
	}) {
		if err != nil {
			return nil, err
		}
		all = append(all, it)
	}
	return all, nil
}

func names(stats []queueStat) []string {
	out := make([]string, 0, len(stats))
	for _, st := range stats {
		out = append(out, st.QueueName)
	}
	return out
}

func (s *queueStatSteps) statsList(ctx context.Context, client string) error {
	items, next, err := s.statsPage(ctx, 0, nil)
	if err != nil {
		return err
	}
	if next != nil {
		return fmt.Errorf("%w: the default page has a nextCursor", harness.ErrStep)
	}
	if got := names(items); !slices.Equal(got, logicalQueues) {
		return fmt.Errorf("%w: queues %q, want %q", harness.ErrStep, got, logicalQueues)
	}
	for _, it := range items {
		if it.ClientType != client {
			return fmt.Errorf(
				"%w: %s served by %q, want %q",
				harness.ErrStep,
				it.QueueName,
				it.ClientType,
				client,
			)
		}
	}
	return nil
}

func (s *queueStatSteps) statsReport(
	ctx context.Context,
	visible, inFlight int,
	queue string,
) error {
	all, err := s.stats(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(all, func(st queueStat) bool { return st.QueueName == queue })
	if i < 0 {
		return fmt.Errorf("%w: no %s queue in %q", harness.ErrStep, queue, names(all))
	}
	if got := all[i]; got.Visible != visible || got.InFlight != inFlight {
		return fmt.Errorf("%w: %s has %d visible and %d in flight, want %d and %d",
			harness.ErrStep, queue, got.Visible, got.InFlight, visible, inFlight)
	}
	return nil
}

func (s *queueStatSteps) statsPaged(ctx context.Context, limit int) error {
	pages := 0
	var cursor *string
	var got []string
	for {
		items, next, err := s.statsPage(ctx, limit, cursor)
		if err != nil {
			return err
		}
		pages++
		if len(items) > limit {
			return fmt.Errorf(
				"%w: a page of %d, over the limit %d",
				harness.ErrStep,
				len(items),
				limit,
			)
		}
		got = append(got, names(items)...)
		if next == nil {
			break
		}
		cursor = next
	}
	if want := (len(logicalQueues) + limit - 1) / limit; pages != want {
		return fmt.Errorf("%w: %d pages, want %d", harness.ErrStep, pages, want)
	}
	if !slices.Equal(got, logicalQueues) {
		return fmt.Errorf("%w: queues %q, want %q", harness.ErrStep, got, logicalQueues)
	}
	return nil
}
