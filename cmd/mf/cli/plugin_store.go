// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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

// PluginBinDir returns the managed install directory.
func PluginBinDir() (string, error) {
	parent, err := pluginParentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, "bin"), nil
}

// pluginNameFromPath derives a plugin name from a binary path:
// ./bin/mf-digisac -> digisac, and on Windows mf-digisac.exe -> digisac.
func pluginNameFromPath(path string) string {
	base := filepath.Base(path)
	if runtime.GOOS == windowsGOOS && isWindowsExecExt(filepath.Ext(base)) {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return strings.TrimPrefix(base, pluginBinaryPrefix)
}

func isWindowsExecExt(ext string) bool {
	return ext != "" && slices.Contains(windowsExecExts(), strings.ToLower(ext))
}

func windowsExecExts() []string {
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".com;.exe;.bat;.cmd"
	}
	var exts []string
	for _, e := range strings.Split(strings.ToLower(pathext), ";") {
		if e != "" {
			exts = append(exts, e)
		}
	}
	return exts
}

func isWindows() bool { return runtime.GOOS == windowsGOOS }

func copyExecutable(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // src is the path the user asked to install
	if err != nil {
		return fmt.Errorf("open plugin: %w", err)
	}
	defer func() { _ = in.Close() }()
	//nolint:gosec // dst is inside the managed bin dir; plugins must be executable
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create plugin copy: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy plugin: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("write plugin copy: %w", err)
	}
	return nil
}
