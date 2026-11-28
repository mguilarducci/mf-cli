package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli/versioninfo"
	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// NewRootCmd builds the `mf` command tree. Plugins are not part of it: they
// are resolved by MaybeRunPlugin before Cobra runs, and only listed in help.
// reg is the Plugin registry the plugin commands work against.
func NewRootCmd(reg *registry.Registry) *cobra.Command {
	root := &cobra.Command{
		Use:           "mf",
		Short:         "mf aggregates CLI plugins under a single command.",
		Version:       versioninfo.Version + " (" + versioninfo.Commit + ")",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.AddCommand(newPluginGroupCmd(reg))
	usePluginCommandsInRootHelp(root, reg)
	return root
}

// Execute runs the root command and exits non-zero on error.
func Execute(root *cobra.Command) {
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
