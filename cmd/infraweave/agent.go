package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/agent/cli"
)

// agentCmds are the agent's commands: the launcher, the runner it starts
// in each run's container, and the Kubernetes worker controller.
func agentCmds() []*cobra.Command {
	return []*cobra.Command{cli.Launcher(), cli.Runner(), cli.WorkerController()}
}
