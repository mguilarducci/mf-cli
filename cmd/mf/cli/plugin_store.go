// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// Managed plugin storage. The dispatcher resolves `mf-<name>` from PATH,
// period. `mf plugin install` stays additive to that by linking binaries
// into a managed bin dir that main.go prepends to PATH:
//
//	<parent>/bin/mf-<name>   dispatch surface (symlink, or copy as fallback)
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

// PluginDataDir returns the per-plugin data directory. Not created: that is
// the plugin's job on first use.
func PluginDataDir(name string) (string, error) {
	if err := validatePluginName(name); err != nil {
		return "", err
	}
	parent, err := pluginParentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, "data", name), nil
}

// PrependPluginBinDirToPATH makes managed plugins discoverable. Silent on
// failure: PATH-installed plugins still work.
func PrependPluginBinDirToPATH() {
	dir, err := PluginBinDir()
	if err != nil {
		return
	}
	path := os.Getenv("PATH")
	for _, p := range filepath.SplitList(path) {
		if p == dir {
			return
		}
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+path)
}

var pluginNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// validatePluginName rejects names the dispatcher would never invoke:
// empty, flag-shaped, path-like, or with uppercase/odd characters.
func validatePluginName(name string) error {
	if !pluginNameRe.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q: use lowercase letters, digits, '-' or '_'", name)
	}
	return nil
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

// isExecutableFile reports whether path can be run as a plugin. Windows has
// no executable bit, so it goes by extension (PATHEXT), like exec.LookPath.
func isExecutableFile(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	if runtime.GOOS == windowsGOOS {
		return isWindowsExecExt(filepath.Ext(path))
	}
	return info.Mode().Perm()&0o111 != 0
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

// managedBinaryName is the file name a plugin gets in the managed bin dir.
// On Windows it keeps the source's extension (.exe, .bat, ...) so
// exec.LookPath can still find it through PATHEXT.
func managedBinaryName(name, src string) string {
	if runtime.GOOS == windowsGOOS {
		return pluginBinaryPrefix + name + strings.ToLower(filepath.Ext(src))
	}
	return pluginBinaryPrefix + name
}

// installLocalPlugin links src into the managed bin dir as mf-<name>.
// A symlink keeps a dev build live across rebuilds; a copy is the fallback
// for filesystems that refuse symlinks.
func installLocalPlugin(src, name string) (string, error) {
	if err := validatePluginName(name); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", src, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat plugin: %w", err)
	}
	if !isExecutableFile(abs, info) {
		return "", fmt.Errorf("%s is not an executable file", src)
	}

	binDir, err := PluginBinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}
	dst := filepath.Join(binDir, managedBinaryName(name, abs))
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("replace existing plugin: %w", err)
	}
	if err := os.Symlink(abs, dst); err == nil {
		return dst, nil
	}
	return dst, copyExecutable(abs, dst)
}

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

// removePlugin deletes the managed entry for name. Data is kept unless purge.
func removePlugin(name string, purge bool) error {
	if err := validatePluginName(name); err != nil {
		return err
	}
	binDir, err := PluginBinDir()
	if err != nil {
		return err
	}
	candidates := []string{pluginBinaryPrefix + name}
	if runtime.GOOS == windowsGOOS {
		candidates = nil
		for _, ext := range windowsExecExts() {
			candidates = append(candidates, pluginBinaryPrefix+name+ext)
		}
	}
	removed := false
	for _, c := range candidates {
		err := os.Remove(filepath.Join(binDir, c))
		switch {
		case err == nil:
			removed = true
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("remove plugin: %w", err)
		}
	}
	if !removed {
		return fmt.Errorf("plugin %q is not installed in %s", name, binDir)
	}
	if purge {
		dataDir, err := PluginDataDir(name)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(dataDir); err != nil {
			return fmt.Errorf("remove plugin data: %w", err)
		}
	}
	return nil
}

type pluginEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Target  string `json:"target,omitempty"` // symlink target, if any
	Managed bool   `json:"managed"`
}

// discoverPlugins lists every mf-<name> executable on PATH, first match wins,
// sorted by name. Managed entries are flagged.
func discoverPlugins() []pluginEntry {
	binDir, err := PluginBinDir()
	if err != nil {
		binDir = "" // no managed dir: every entry is a plain PATH plugin
	}
	seen := map[string]bool{}
	var out []pluginEntry
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), pluginBinaryPrefix) {
				continue
			}
			name := pluginNameFromPath(e.Name())
			if seen[name] || validatePluginName(name) != nil {
				continue
			}
			path := filepath.Join(dir, e.Name())
			info, err := os.Stat(path) //nolint:gosec // path is a PATH entry joined with a validated mf-<name>
			if err != nil || !isExecutableFile(path, info) {
				continue
			}
			seen[name] = true
			target, err := os.Readlink(path)
			if err != nil {
				target = "" // not a symlink
			}
			out = append(out, pluginEntry{Name: name, Path: path, Target: target, Managed: dir == binDir})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
