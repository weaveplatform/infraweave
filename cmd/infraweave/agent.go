package main

import (
	"github.com/spf13/cobra"

	"github.com/weaveplatform/infraweave/internal/agent/cli"
)

// agentCmds are the agent's commands: the launcher, and the runner it
// starts in each run's container.
func agentCmds() []*cobra.Command { return []*cobra.Command{cli.Launcher(), cli.Runner()} }
