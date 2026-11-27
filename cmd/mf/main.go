package main

import (
	"os"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli"
)

func main() {
	rootCmd := cli.NewRootCmd()

	// Make plugins installed with `mf plugin install` discoverable by the
	// kubectl-style dispatcher, then try to run `mf <name>` as `mf-<name>`
	// before falling through to the built-in commands.
	cli.PrependPluginBinDirToPATH()
	if handled, code := cli.MaybeRunPlugin(rootCmd, os.Args[1:]); handled {
		os.Exit(code)
	}

	cli.Execute(rootCmd)
}
