package main

import (
	"context"
	"os"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli"
	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func main() {
	// Snapshot the environment before anything changes it.
	reg := registry.New(registry.EnvFromProcess())
	rootCmd := cli.NewRootCmd(reg)

	// Make plugins installed with `mf plugin install` discoverable by the
	// kubectl-style dispatcher, then try to run `mf <name>` as `mf-<name>`
	// before falling through to the built-in commands.
	cli.PrependPluginBinDirToPATH()
	// No cancellation: Ctrl-C reaches the plugin through the terminal's
	// process group, and the plugin decides how to shut down.
	if handled, code := cli.MaybeRunPlugin(context.Background(), reg, rootCmd, os.Args[1:]); handled {
		os.Exit(code)
	}

	cli.Execute(rootCmd)
}
