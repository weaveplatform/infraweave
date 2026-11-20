package policies

import (
	"fmt"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

func registerAttachments(sc *godog.ScenarioContext, w *harness.World) {
	sc.Step(`^the policy and attachment have distinct ULIDs$`, func() error {
		policy, err := kernel.ParseULID(w.IDs["policy"])
		if err != nil {
			return fmt.Errorf("policy identity: %w", err)
		}
		attachment, err := kernel.ParseULID(w.IDs["attachment"])
		if err != nil {
			return fmt.Errorf("attachment identity: %w", err)
		}
		if policy == attachment {
			return fmt.Errorf("%w: attachment reused policy identity", harness.ErrStep)
		}
		return nil
	})
}
