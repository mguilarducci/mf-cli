// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli/versioninfo"
	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// External-command resolution, kubectl-style (same model as entireio/cli).
// When the user invokes `mf <name>` and <name> isn't a built-in subcommand,
// look for an `mf-<name>` binary on PATH and exec it with the remaining args.
// Stdio and exit codes pass through.
const pluginBinaryPrefix = "mf-"

const windowsGOOS = "windows"

// MaybeRunPlugin returns (true, exitCode) when a plugin was resolved and run,
// and (false, 0) when the caller should fall through to Cobra.
func MaybeRunPlugin(ctx context.Context, reg *registry.Registry, root *cobra.Command, args []string) (handled bool, exitCode int) {
	binPath, pluginArgs, ok := resolvePlugin(root, args)
	if !ok {
		return false, 0
	}
	return true, runPlugin(ctx, reg, args[0], binPath, pluginArgs)
}

func resolvePlugin(root *cobra.Command, args []string) (binPath string, pluginArgs []string, ok bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	name := args[0]
	if registry.ValidateName(name) != nil {
		return "", nil, false
	}
	// Built-ins (including Cobra's lazily-added help/completion) win.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd(args...)
	if cmd, _, err := root.Find(args); err == nil && cmd != root {
		return "", nil, false
	}
	binPath, err := exec.LookPath(pluginBinaryPrefix + name)
	if err != nil {
		return "", nil, false
	}
	return binPath, args[1:], true
}

func runPlugin(ctx context.Context, reg *registry.Registry, name, binPath string, args []string) int {
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	extras := []string{
		"MF_CLI_VERSION=" + versioninfo.Version,
		"MF_PLUGIN_NAME=" + name,
	}
	if dataDir, err := reg.DataDir(name); err == nil {
		extras = append(extras, "MF_PLUGIN_DATA_DIR="+dataDir)
	}
	cmd.Env = pluginEnv(os.Environ(), name, extras...)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Failed to run plugin %s: %v\n", pluginBinaryPrefix+name, err)
		return 1
	}
	return 0
}
