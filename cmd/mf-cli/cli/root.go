package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version will be linked by an ldflag during build.
var Version = "v0.1.0-dev"

// NewRootCmd builds the `mf` command tree. Plugins are not part of it: they
// are resolved by MaybeRunPlugin before Cobra runs, and only listed in help.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mf",
		Short:         "mf aggregates CLI plugins under a single command.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run:           func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	root.AddCommand(newPluginGroupCmd())
	usePluginCommandsInRootHelp(root)
	return root
}

// Execute runs the root command and exits non-zero on error.
func Execute(root *cobra.Command) {
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
