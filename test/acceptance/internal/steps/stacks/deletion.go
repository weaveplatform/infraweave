package stacks

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

// stackDeletionSteps wait for a stack's deletion, which a destroy run's
// end commits on the server's own time.
type stackDeletionSteps struct {
	w *harness.World
}

func (st *stackDeletionSteps) register(sc *godog.ScenarioContext) {
	sc.Step(`^token "(\w+)" eventually finds no stack "([a-z0-9-]+)" through ([AB])$`, st.gone)
}

// gone reads the stack slug with token until it answers 404; any answer
// but the stack or a 404 fails the step.
func (st *stackDeletionSteps) gone(ctx context.Context, token, slug, replica string) error {
	//nolint:wrapcheck // names what it waited for
	return deps.Eventually(
		ctx,
		"stack "+slug+" to be deleted",
		func(ctx context.Context) (bool, error) {
			if err := st.w.SendAs(ctx, `token "`+token+`"`, http.MethodGet, "/api/v1/stacks/"+slug,
				"", replica); err != nil {
				return false, err
			}
			switch last := st.w.Last(); last.Status {
			case http.StatusOK:
				return false, nil
			case http.StatusNotFound:
				return true, nil
			default:
				return false, fmt.Errorf("%w: stack %s answered %d %s", harness.ErrStep, slug,
					last.Status, last.Body)
			}
		},
	)
}
