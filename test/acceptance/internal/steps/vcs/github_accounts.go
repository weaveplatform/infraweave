package vcs

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerAccountSteps(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the answer's optional "(\w+)" is unset$`, func(name string) error {
		r := w.Last()
		var fields map[string]json.RawMessage
		if r.Status < 200 || r.Status >= 300 || json.Unmarshal(r.Body, &fields) != nil || fields == nil {
			return fmt.Errorf("%w: no successful JSON object in status %d", harness.ErrStep, r.Status)
		}
		value, present := fields[name]
		if present && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%w: top-level optional field %q is set", harness.ErrStep, name)
		}
		return nil
	})
}
