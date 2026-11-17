package exec

import (
	"context"
	"fmt"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// preparePhase reports repository diagnostics before setup can start a shell.
// The Boolean preserves the distinction between no refusal and an explicitly
// present empty refusal, as well as preparation failures after diagnostics.
func (r *run) preparePhase(ctx context.Context, state wire.WorkerState) (string, bool) {
	if state == wire.WorkerStateINITIALIZING {
		if notices := r.h.Spec.Notices; notices != nil {
			for _, notice := range *notices {
				_, _ = fmt.Fprintln(r.log, "infraweave: .infraweave/config.yml: "+notice)
			}
		}
		if refusal := r.h.Spec.Refusal; refusal != nil {
			_, _ = fmt.Fprintln(r.log, "infraweave: .infraweave/config.yml: "+*refusal)
			return *refusal, true
		}
	}
	if !r.ready {
		r.prepare(ctx)
	}
	return r.failure, r.failure != ""
}
