package main

import (
	"context"
	"os"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli"
	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

func main() {
	// Snapshot the environment once; nothing in mf changes it.
	reg := registry.New(registry.EnvFromProcess())
	rootCmd := cli.NewRootCmd(reg)

	// Try to run `mf <name>` as the plugin the registry resolves before
	// falling through to the built-in commands.
	// No cancellation: Ctrl-C reaches the plugin through the terminal's
	// process group, and the plugin decides how to shut down.
	if handled, code := cli.MaybeRunPlugin(context.Background(), reg, rootCmd, os.Args[1:]); handled {
		os.Exit(code)
	}

	cli.Execute(rootCmd)
}
