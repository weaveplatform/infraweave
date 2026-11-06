package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"
)

// accountSteps are the steps of the Account reads, on authSteps' servers
// and named tokens.
type accountSteps struct{ a *authSteps }

func (s *accountSteps) register(sc *godog.ScenarioContext) {
	sc.Step(
		`^the account reads through ([AB]) with token "(\w+)" answer the (home|sibling|stranger) account, type "([A-Z]+)" and runner image "([^"]+)"$`,
		s.reads,
	)
}

// reads checks each Account read against the tenant's seeded account,
// whose name is its slug.
func (s *accountSteps) reads(ctx context.Context, replica, tok, who, typ, image string) error {
	t := tenants[who]
	for path, want := range map[string]string{
		"/api/v1/id":   t.account,
		"/api/v1/name": t.slug,
		"/api/v1/type": typ,
		"/api/v1/default-private-worker-pool-runner-image": image,
	} {
		r, err := s.a.do(ctx, replica, tok, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var got string
		if err := json.Unmarshal(
			r.body,
			&got,
		); err != nil || r.status != http.StatusOK ||
			got != want {
			return fmt.Errorf(
				"%w: %s answered %d %s, want %q",
				errStep,
				path,
				r.status,
				r.body,
				want,
			)
		}
	}
	return nil
}
