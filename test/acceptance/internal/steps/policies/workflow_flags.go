package policies

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
	"github.com/weaveplatform/infraweave/test/deps"
)

func registerWorkflowFlags(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^token "(\w+)" eventually reads run "(\w+)" flags '([^']*)' through ([AB])$`,
		func(ctx context.Context, token, id, expected, replica string) error {
			var want []string
			if err := json.Unmarshal([]byte(w.Resolve(expected)), &want); err != nil {
				return fmt.Errorf("decode expected workflow flags: %w", err)
			}
			return deps.Eventually(ctx, "committed public workflow flags", func(ctx context.Context) (bool, error) {
				response, err := w.Do(ctx, replica, token, http.MethodGet, "/api/v1/runs/"+url.PathEscape(w.IDs[id]), nil)
				if err != nil {
					return false, err
				}
				if response.Status != http.StatusOK {
					return false, fmt.Errorf("%w: workflow run read answered %d", harness.ErrStep, response.Status)
				}
				var run api.Run
				if err := json.Unmarshal(response.Body, &run); err != nil {
					return false, fmt.Errorf("decode workflow flags: %w", err)
				}
				return run.Flags != nil && slices.Equal(*run.Flags, want), nil
			})
		})
}
