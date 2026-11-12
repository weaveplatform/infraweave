package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

// accountSteps are the steps of the Account reads, on authSteps' servers
// and named tokens.
type accountSteps struct {
	w *harness.World
}

func (s *accountSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the account reads through ([AB]) with token "(\w+)" answer the (home|sibling|stranger) account, type "([A-Z]+)" and runner image "([^"]+)"$`,
		s.reads,
	)
}

// reads checks each Account read against the tenant's seeded account,
// whose name is its slug.
func (s *accountSteps) reads(ctx context.Context, replica, tok, who, typ, image string) error {
	t := harness.Tenants[who]
	for path, want := range map[string]string{
		"/api/v1/id":   t.Account,
		"/api/v1/name": t.Slug,
		"/api/v1/type": typ,
		"/api/v1/default-private-worker-pool-runner-image": image,
	} {
		r, err := s.w.Do(ctx, replica, tok, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var got string
		if err := json.Unmarshal(
			r.Body,
			&got,
		); err != nil || r.Status != http.StatusOK ||
			got != want {
			return fmt.Errorf(
				"%w: %s answered %d %s, want %q",
				harness.ErrStep,
				path,
				r.Status,
				r.Body,
				want,
			)
		}
	}
	return nil
}
