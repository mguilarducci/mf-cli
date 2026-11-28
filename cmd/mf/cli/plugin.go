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
	"strings"

	"github.com/spf13/cobra"

	"github.com/mguilarducci/mf-cli/cmd/mf/cli/versioninfo"
	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// External-command resolution, kubectl-style (same model as entireio/cli).
// When the user invokes `mf <name>` and <name> isn't a built-in subcommand,
// the Plugin registry resolves `mf-<name>` on its Search path and it runs
// with the remaining args. Stdio and exit codes pass through.

const windowsGOOS = "windows"

// MaybeRunPlugin returns (true, exitCode) when a plugin was resolved and run,
// and (false, 0) when the caller should fall through to Cobra.
func MaybeRunPlugin(ctx context.Context, reg *registry.Registry, root *cobra.Command, args []string) (handled bool, exitCode int) {
	plugin, pluginArgs, ok := resolvePlugin(reg, root, args)
	if !ok {
		return false, 0
	}
	return true, runPlugin(ctx, reg, plugin, pluginArgs)
}

func resolvePlugin(reg *registry.Registry, root *cobra.Command, args []string) (plugin registry.Plugin, pluginArgs []string, ok bool) {
	if len(args) == 0 || registry.ValidateName(args[0]) != nil {
		return registry.Plugin{}, nil, false
	}
	// Built-ins (including Cobra's lazily-added help/completion) win.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd(args...)
	if cmd, _, err := root.Find(args); err == nil && cmd != root {
		return registry.Plugin{}, nil, false
	}
	plugin, ok = reg.Resolve(args[0])
	if !ok {
		return registry.Plugin{}, nil, false
	}
	return plugin, args[1:], true
}

func runPlugin(ctx context.Context, reg *registry.Registry, plugin registry.Plugin, args []string) int {
	name := plugin.Name
	cmd := exec.CommandContext(ctx, plugin.Path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// The plugin's PATH is the Search path, so it sees the registry first
	// and can call other mf-* plugins directly.
	extras := []string{
		"PATH=" + reg.SearchPath(),
		"MF_CLI_VERSION=" + versioninfo.Version,
		"MF_PLUGIN_NAME=" + name,
	}
	if dataDir, err := reg.DataDir(name); err == nil {
		extras = append(extras, "MF_PLUGIN_DATA_DIR="+dataDir)
	}
	cmd.Env = pluginEnv(withoutPath(os.Environ()), name, extras...)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Failed to run plugin %s: %v\n", registry.BinaryPrefix+name, err)
		return 1
	}
	return 0
}

// withoutPath drops PATH (any case, as Windows spells it Path) so the
// Search path replaces it.
func withoutPath(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if key, _, _ := strings.Cut(kv, "="); !strings.EqualFold(key, "PATH") {
			out = append(out, kv)
		}
	}
	return out
}
