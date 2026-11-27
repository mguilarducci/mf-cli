package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli/versioninfo"
)

// NewRootCmd builds the `mf` command tree. Plugins are not part of it: they
// are resolved by MaybeRunPlugin before Cobra runs, and only listed in help.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mf",
		Short:         "mf aggregates CLI plugins under a single command.",
		Version:       versioninfo.Version + " (" + versioninfo.Commit + ")",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
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
