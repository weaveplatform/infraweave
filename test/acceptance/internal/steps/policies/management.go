package policies

import (
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerManagement(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the policy refusal locates the body source$`, func() error {
		var problem struct {
			Errors []struct {
				Pointer string `json:"pointer"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
				Range   *struct {
					Start sourcePosition `json:"start"`
					End   sourcePosition `json:"end"`
				} `json:"range"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(w.Last().Body, &problem); err != nil {
			return fmt.Errorf("decode policy refusal: %w", err)
		}
		for _, field := range problem.Errors {
			if field.Pointer != "/body" || field.Reason != "rule" || field.Message == "" || field.Range == nil {
				continue
			}
			start, end := field.Range.Start, field.Range.End
			if start.Line > 0 && start.Column > 0 && (end.Line > start.Line || (end.Line == start.Line && end.Column > start.Column)) {
				return nil
			}
		}
		return fmt.Errorf("%w: policy refusal lacks a source field/range: %s", harness.ErrStep, w.Last().Body)
	})
}

type sourcePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
