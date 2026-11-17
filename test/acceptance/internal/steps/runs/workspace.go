package runs

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/weaveplatform/infraweave/test/acceptance/internal/harness"
)

var workspaceANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// workspaceEvidence reads the real planning log's working directory and
// mounted output, without printing unrelated lines or configuration.
func (r *runSteps) workspaceEvidence(ctx context.Context, token, run, replica string) error {
	text, err := logText(ctx, r.w, token, run, "PLANNING", replica)
	if err != nil {
		return err
	}
	cwd, mounted := false, false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(workspaceANSI.ReplaceAllString(line, ""))
		switch {
		case line == "/mnt/workspace/source":
			cwd = true
		case strings.Join(strings.Fields(line), " ") == `+ where = "mounted"`:
			mounted = true
		default:
			continue
		}
		fmt.Printf("acceptance: workspace %s PLANNING: %s\n", r.w.IDs[run], line)
	}
	if !cwd || !mounted {
		return fmt.Errorf("%w: planning log has cwd=%t and mounted output=%t", harness.ErrStep, cwd, mounted)
	}
	return nil
}
