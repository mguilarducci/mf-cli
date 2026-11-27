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
		return "", err
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
// ./bin/mf-digisac -> digisac.
func pluginNameFromPath(path string) string {
	return strings.TrimPrefix(filepath.Base(path), pluginBinaryPrefix)
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
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s is not an executable file", src)
	}

	binDir, err := PluginBinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(binDir, pluginBinaryPrefix+name)
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Symlink(abs, dst); err == nil {
		return dst, nil
	}
	return dst, copyExecutable(abs, dst)
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
	if err := os.Remove(filepath.Join(binDir, pluginBinaryPrefix+name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("plugin %q is not installed in %s", name, binDir)
		}
		return err
	}
	if purge {
		dataDir, err := PluginDataDir(name)
		if err != nil {
			return err
		}
		return os.RemoveAll(dataDir)
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
	binDir, _ := PluginBinDir()
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
			info, err := os.Stat(path) // follows symlinks
			if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
				continue
			}
			seen[name] = true
			target, _ := os.Readlink(path)
			out = append(out, pluginEntry{Name: name, Path: path, Target: target, Managed: dir == binDir})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
