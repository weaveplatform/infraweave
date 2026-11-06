package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
)

type fakeStats struct {
	stats []queue.Stat
	err   error
}

func (f fakeStats) Stats(context.Context) ([]queue.Stat, error) { return f.stats, f.err }

// depths are one Stat per logical queue, in queue.Queues order: queue i
// has i visible and 10*i in flight.
func depths() []queue.Stat {
	out := make([]queue.Stat, 0, len(queue.Queues))
	for i, n := range queue.Queues {
		out = append(out, queue.Stat{Queue: n, Visible: i, InFlight: 10 * i})
	}
	return out
}

func statsPage(t *testing.T, h http.Handler, query url.Values) api.MessageQueueStatsPage {
	t.Helper()
	rec := send(h, http.MethodGet, "/api/v1/message-queue-stats?"+query.Encode(), "192.0.2.1:1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ?%s: %d %s", query.Encode(), rec.Code, rec.Body)
	}
	var p api.MessageQueueStatsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUnitMessageQueueStats(t *testing.T) {
	c := config.Defaults().Server
	h := API(discard(), c, memory(c), Domains{}, fakeStats{stats: depths()}, Blobs{})

	all := statsPage(t, h, nil)
	if all.NextCursor != nil || len(all.Items) != len(queue.Queues) {
		t.Fatalf("default page: %d items, next %v; want every queue on one page",
			len(all.Items), all.NextCursor)
	}
	for i, it := range all.Items {
		want := api.MessageQueueStats{
			ClientType: "postgres", QueueName: string(queue.Queues[i]),
			Visible: int32(i), InFlight: int32(10 * i), //nolint:gosec // small test values
		}
		if it != want {
			t.Errorf("item %d = %+v, want %+v", i, it, want)
		}
	}

	var names []string
	q := url.Values{"limit": {"3"}}
	for pages := 0; ; pages++ {
		p := statsPage(t, h, q)
		for _, it := range p.Items {
			names = append(names, it.QueueName)
		}
		if p.NextCursor == nil {
			if pages != 2 {
				t.Errorf("%d pages of 3, want 3", pages+1)
			}
			break
		}
		q.Set("cursor", *p.NextCursor)
	}
	want := make([]string, 0, len(queue.Queues))
	for _, n := range queue.Queues {
		want = append(want, string(n))
	}
	if !slices.Equal(names, want) {
		t.Errorf("paged through %q, want %q", names, want)
	}

	// A cursor past the end, which no page hands out, gives an empty page.
	past, err := page.Encode(queueStatsList, kernel.Cursor[int]{Key: 99, ID: "maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if p := statsPage(
		t,
		h,
		url.Values{"cursor": {past}},
	); len(p.Items) != 0 ||
		p.NextCursor != nil {
		t.Errorf("past the end: %+v", p)
	}
}

func TestUnitMessageQueueStatsProblems(t *testing.T) {
	c := config.Defaults().Server
	other, err := page.Encode("stacks", kernel.Cursor[int]{Key: 1, ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ok := API(discard(), c, memory(c), Domains{}, fakeStats{stats: depths()}, Blobs{})
	for _, cursor := range []string{"not base64!", other} {
		wantTyped(
			t,
			send(ok, http.MethodGet, "/api/v1/message-queue-stats?cursor="+url.QueryEscape(cursor),
				"192.0.2.1:1", ""),
			problem.InvalidCursor,
		)
	}
	down := API(discard(), c, memory(c), Domains{},
		fakeStats{err: fmt.Errorf("stats: %w", kernel.ErrUnavailable)}, Blobs{})
	wantTyped(t, send(down, http.MethodGet, "/api/v1/message-queue-stats", "192.0.2.1:1", ""),
		problem.ServiceUnavailable)
	broken := API(
		discard(),
		c,
		memory(c),
		Domains{},
		fakeStats{err: errors.New("stats: broken")},
		Blobs{},
	)
	if rec := send(
		broken,
		http.MethodGet,
		"/api/v1/message-queue-stats",
		"192.0.2.1:1",
		"",
	); rec.Code != http.StatusInternalServerError {
		t.Errorf("store failure: %d, want 500", rec.Code)
	}
}

func TestUnitQueueCount(t *testing.T) {
	if got := count(1 << 40); got != 1<<31-1 {
		t.Errorf("count(2^40) = %d, want the int32 maximum", got)
	}
}
