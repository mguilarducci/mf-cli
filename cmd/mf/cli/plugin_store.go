// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Managed plugin storage. The Plugin registry resolves `mf-<name>` on its
// Search path, which starts with the managed bin dir:
//
//	<parent>/bin/mf-<name>   dispatch surface (symlink, or copy as fallback)
//	<parent>/pkg/<name>/     remote installs: binary + manifest.yml
//	<parent>/data/<name>/    per-plugin durable storage, MF_PLUGIN_DATA_DIR
//
// <parent> is MF_PLUGIN_DIR, else $XDG_DATA_HOME/mf/plugins,
// else ~/.local/share/mf/plugins.

func pluginParentDir() (string, error) {
	if dir := os.Getenv("MF_PLUGIN_DIR"); dir != "" {
		return dir, nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "mf", "plugins"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "mf", "plugins"), nil
}

func isWindows() bool { return runtime.GOOS == windowsGOOS }
