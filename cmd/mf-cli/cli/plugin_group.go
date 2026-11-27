package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newPluginGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"plugins"},
		Short:   "Manage mf plugins (install, list, remove)",
		Long: `A plugin is any executable named mf-<name>. Put it on PATH, or install it
with 'mf plugin install', and run it as 'mf <name>'.`,
		Run: func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newPluginInstallCmd(), newPluginListCmd(), newPluginRemoveCmd())
	return cmd
}

func newPluginInstallCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "install <path>",
		Short: "Install a local plugin executable",
		Long: `Link a local executable into the managed plugin dir. The plugin name is the
file name without the mf- prefix, unless --name is given. The link is a
symlink, so rebuilding the binary updates the installed plugin.`,
		Example: `  mf plugin install ./bin/mf-digisac
  mf plugin install ./some-binary --name foo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				name = pluginNameFromPath(args[0])
			}
			dst, err := installLocalPlugin(args[0], name)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Installed %q at %s\nRun it with: mf %s\n", name, dst, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Plugin name (default: file name without mf- prefix)")
	return cmd
}

func newPluginListCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List plugins found on PATH and in the managed dir",
		RunE: func(cmd *cobra.Command, args []string) error {
			plugins := discoverPlugins()
			if output == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if plugins == nil {
					plugins = []pluginEntry{}
				}
				return enc.Encode(plugins)
			}
			if len(plugins) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No plugins found. Install one with: mf plugin install <path>")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSOURCE\tPATH")
			for _, p := range plugins {
				source, path := "PATH", p.Path
				if p.Managed {
					source = "managed"
				}
				if p.Target != "" {
					path = p.Path + " -> " + p.Target
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, source, path)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "table", "Output format: table or json")
	return cmd
}

func newPluginRemoveCmd() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Remove a plugin from the managed dir",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := removePlugin(args[0], purge); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Removed %q\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "Also delete the plugin's data dir")
	return cmd
}
