package workers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// projectionSteps check the pool projections: the stacks a pool's users
// list, the runs a queue lists with their positions, and the billing
// cycles a usage read selects.
type projectionSteps struct{ w *harness.World }

func (p *projectionSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^the answer lists the pool users "([^"]*)"$`, p.users)
	sc.Step(`^the answer lists the queued runs "([^"]*)"$`, p.queued)
	sc.Step(`^the answer selects the cycle (\d+) of (\d+) calendar months$`, p.cycles)
}

// users checks the slugs, comma-separated, of the stacks the last answer
// lists in order: a search's edges, or a pool's users.
func (p *projectionSteps) users(want string) error {
	type user struct {
		StackID string `json:"stackId"`
	}
	var page struct {
		Edges []struct {
			Node user `json:"node"`
		} `json:"edges"`
		Users []user `json:"users"`
	}
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got := []string{}
	for _, e := range page.Edges {
		got = append(got, e.Node.StackID)
	}
	for _, u := range page.Users {
		got = append(got, u.StackID)
	}
	if w := p.w.SlugList(want); !slices.Equal(got, w) {
		return fmt.Errorf("%w: the pool users are %v, want %v", harness.ErrStep, got, w)
	}
	return nil
}

// queued checks the runs a queue search answers, in order, each written
// as the name its ID was kept under and its position: "first@1, second@2".
func (p *projectionSteps) queued(want string) error {
	var page struct {
		Edges []struct {
			Node struct {
				Position int `json:"position"`
				Run      struct {
					ID string `json:"id"`
				} `json:"run"`
			} `json:"node"`
		} `json:"edges"`
	}
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	got, wanted := []string{}, []string{}
	for _, e := range page.Edges {
		got = append(got, e.Node.Run.ID+"@"+strconv.Itoa(e.Node.Position))
	}
	for entry := range strings.SplitSeq(want, ",") {
		if name, at, ok := strings.Cut(strings.TrimSpace(entry), "@"); ok {
			wanted = append(wanted, p.w.IDs[name]+"@"+at)
		}
	}
	if !slices.Equal(got, wanted) {
		return fmt.Errorf("%w: the queue is %v, want %v", harness.ErrStep, got, wanted)
	}
	return nil
}

// cycles checks a usage read: it answers the cycle ago cycles back, and
// selects n cycles, newest first, each a calendar month in UTC that ends
// where the newer one starts, the first holding now.
func (p *projectionSteps) cycles(ago, n int) error {
	var usage struct {
		Cycle      json.RawMessage   `json:"cycle"`
		Selectable []json.RawMessage `json:"selectableCycles"`
	}
	r := p.w.Last()
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &usage) != nil || len(usage.Selectable) != n {
		return fmt.Errorf("%w: want %d selectable cycles: %d %s", harness.ErrStep, n, r.Status, r.Body)
	}
	read := func(raw json.RawMessage) (int, time.Time, time.Time, error) {
		var c struct {
			CyclesAgo int   `json:"cyclesAgo"`
			Start     int64 `json:"start"`
			End       int64 `json:"end"`
		}
		err := json.Unmarshal(raw, &c)
		return c.CyclesAgo, time.Unix(c.Start, 0).UTC(), time.Unix(c.End, 0).UTC(), err
	}
	if got, _, _, err := read(usage.Cycle); err != nil || got != ago {
		return fmt.Errorf("%w: the cycle is not %d cycles ago: %s", harness.ErrStep, ago, r.Body)
	}
	newer := time.Time{}
	for i, raw := range usage.Selectable {
		got, start, end, err := read(raw)
		switch {
		case err != nil, got != i, start.Day() != 1, start.Hour() != 0, !start.AddDate(0, 1, 0).Equal(end):
			return fmt.Errorf("%w: selectable cycle %d is no calendar month: %s", harness.ErrStep, i, raw)
		case i == 0 && (!time.Now().After(start) || !time.Now().Before(end)):
			return fmt.Errorf("%w: the current cycle does not hold now: %s", harness.ErrStep, raw)
		case i > 0 && !end.Equal(newer):
			return fmt.Errorf("%w: selectable cycle %d does not end where %d starts", harness.ErrStep, i, i-1)
		}
		newer = start
	}
	return nil
}
