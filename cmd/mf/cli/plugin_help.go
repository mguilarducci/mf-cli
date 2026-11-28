// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// usePluginCommandsInRootHelp appends a "Plugin Commands" section to the
// root help so the Plugins on the Search path show up next to the built-ins.
func usePluginCommandsInRootHelp(root *cobra.Command, reg *registry.Registry) {
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		defaultHelp(cmd, args)
		if cmd != root {
			return
		}
		if section := formatPluginHelpSection(root, reg.List()); section != "" {
			fmt.Fprint(cmd.OutOrStdout(), section)
		}
	})
}

func formatPluginHelpSection(root *cobra.Command, plugins []registry.Plugin) string {
	var b strings.Builder
	for _, p := range plugins {
		if shadowsBuiltinCommand(root, p.Name) {
			continue
		}
		fmt.Fprintf(&b, "  %-11s %s\n", p.Name, "(plugin: "+registry.BinaryPrefix+p.Name+")")
	}
	if b.Len() == 0 {
		return ""
	}
	return "\nPlugin Commands:\n" + b.String()
}

// shadowsBuiltinCommand reports whether a built-in owns this name, in which
// case the plugin is unreachable as `mf <name>`.
func shadowsBuiltinCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return true
		}
	}
	return false
}
