package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// availabilitySteps read a worker's availableAt through the public worker
// search: it is absent while the worker holds a run, and once the worker
// reports itself idle again it names a time no earlier than the read that
// saw it busy. The server sets it from its own clock, which is the host's
// for an acceptance process, so the busy read's host time bounds it; the
// one-second allowance covers the API's whole-second timestamp.
type availabilitySteps struct {
	w *harness.World
	// busySince is, per worker name, the host time a read first saw the
	// worker busy.
	busySince map[string]time.Time
}

func (a *availabilitySteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^token "(\w+)" eventually reads the worker "(\w+)" of the pool "(\w+)" busy, with no availableAt, through ([AB])$`,
		a.busy,
	)
	sc.Step(
		`^token "(\w+)" eventually reads the worker "(\w+)" of the pool "(\w+)" as "(IDLE|DRAINED)", available since it was read busy, through ([AB])$`,
		a.available,
	)
}

// workerNode is the part of a searched worker these steps read.
type workerNode struct {
	ID          string `json:"id"`
	Busy        bool   `json:"busy"`
	Status      string `json:"status"`
	AvailableAt *int64 `json:"availableAt"`
}

// read answers the worker name of pool as token reads it through replica,
// and whether the search lists it.
func (a *availabilitySteps) read(ctx context.Context, token, name, pool, replica string) (workerNode, bool, error) {
	if err := a.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet,
		"/api/v1/worker-pools/"+a.w.IDs[pool]+"/search-workers?input=%7B%7D", "", replica); err != nil {
		return workerNode{}, false, err
	}
	r := a.w.Last()
	var page struct {
		Edges []struct {
			Node workerNode `json:"node"`
		} `json:"edges"`
	}
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &page) != nil {
		return workerNode{}, false, fmt.Errorf("%w: answered %d %s", harness.ErrStep, r.Status, r.Body)
	}
	for _, e := range page.Edges {
		if e.Node.ID == a.w.IDs[name] {
			return e.Node, true, nil
		}
	}
	return workerNode{}, false, nil
}

func (a *availabilitySteps) busy(ctx context.Context, token, name, pool, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the worker "+name+" to be busy", func(ctx context.Context) (bool, error) {
		seen := time.Now()
		n, listed, err := a.read(ctx, token, name, pool, replica)
		if err != nil || !listed || !n.Busy {
			return false, err
		}
		if n.AvailableAt != nil {
			return false, fmt.Errorf("%w: busy worker %s answers availableAt %d", harness.ErrStep, name, *n.AvailableAt)
		}
		if a.busySince == nil {
			a.busySince = map[string]time.Time{}
		}
		a.busySince[name] = seen
		return true, nil
	})
}

func (a *availabilitySteps) available(ctx context.Context, token, name, pool, status, replica string) error {
	since, ok := a.busySince[name]
	if !ok {
		return fmt.Errorf("%w: no read saw the worker %s busy", harness.ErrStep, name)
	}
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(ctx, "the worker "+name+" to be available", func(ctx context.Context) (bool, error) {
		n, listed, err := a.read(ctx, token, name, pool, replica)
		if err != nil || !listed || n.Busy || n.Status != status || n.AvailableAt == nil {
			return false, err
		}
		if at := time.Unix(*n.AvailableAt, 0); at.Before(since.Add(-time.Second)) {
			return false, fmt.Errorf("%w: worker %s available at %s, before it was read busy at %s",
				harness.ErrStep, name, at.UTC(), since.UTC())
		}
		return true, nil
	})
}
