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
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/mguilarducci/mf-cli/cmd/mf/internal/registry"
)

// Managed plugin storage. The dispatcher resolves `mf-<name>` from PATH,
// period. `mf plugin install` stays additive to that by linking binaries
// into a managed bin dir that main.go prepends to PATH:
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

// pluginDataDir is the data dir removePlugin purges. Launched plugins get
// theirs from the registry.
func pluginDataDir(name string) (string, error) {
	if err := registry.ValidateName(name); err != nil {
		return "", err
	}
	parent, err := pluginParentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, "data", name), nil
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

func isWindows() bool { return runtime.GOOS == windowsGOOS }

// managedEntries returns the bin-dir entries for name that exist: one on
// Unix, any PATHEXT variant on Windows.
func managedEntries(name string) ([]string, error) {
	binDir, err := PluginBinDir()
	if err != nil {
		return nil, err
	}
	candidates := []string{pluginBinaryPrefix + name}
	if isWindows() {
		candidates = nil
		for _, ext := range windowsExecExts() {
			candidates = append(candidates, pluginBinaryPrefix+name+ext)
		}
	}
	var out []string
	for _, c := range candidates {
		p := filepath.Join(binDir, c)
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// ensureInstallable refuses to replace an installed plugin unless force, and
// with force clears the old entry and package so nothing stale is left.
func ensureInstallable(name string, force bool) error {
	existing, err := managedEntries(name)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return nil
	}
	if !force {
		return fmt.Errorf("plugin %q already installed at %s; use --force to replace", name, existing[0])
	}
	for _, p := range existing {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("replace existing plugin: %w", err)
		}
	}
	pkgDir, err := PluginPkgDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		return fmt.Errorf("remove previous plugin package: %w", err)
	}
	return nil
}

// linkIntoBinDir makes target runnable as `mf <name>`. A symlink keeps a dev
// build live across rebuilds; a copy is the fallback for filesystems (or
// Windows accounts) that refuse symlinks.
func linkIntoBinDir(target, name string) (string, error) {
	binDir, err := PluginBinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}
	dst := filepath.Join(binDir, managedBinaryName(name, target))
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("replace existing plugin: %w", err)
	}
	if err := os.Symlink(target, dst); err == nil {
		return dst, nil
	}
	return dst, copyExecutable(target, dst)
}

// installLocalPlugin links src into the managed bin dir as mf-<name>.
func installLocalPlugin(src, name string, force bool) (string, error) {
	if err := registry.ValidateName(name); err != nil {
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
	if err := ensureInstallable(name, force); err != nil {
		return "", err
	}
	return linkIntoBinDir(abs, name)
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

// removePlugin deletes the managed entry and package for name. Data is
// kept unless purge.
func removePlugin(name string, purge bool) error {
	if err := registry.ValidateName(name); err != nil {
		return err
	}
	existing, err := managedEntries(name)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		binDir, _ := PluginBinDir() //nolint:errcheck // managedEntries already resolved it
		return fmt.Errorf("plugin %q is not installed in %s", name, binDir)
	}
	for _, p := range existing {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove plugin: %w", err)
		}
	}
	pkgDir, err := PluginPkgDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		return fmt.Errorf("remove plugin package: %w", err)
	}
	if purge {
		dataDir, err := pluginDataDir(name)
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
	Name     string          `json:"name"`
	Path     string          `json:"path"`
	Target   string          `json:"target,omitempty"` // symlink target, if any
	Managed  bool            `json:"managed"`
	Manifest *PluginManifest `json:"manifest,omitempty"` // remote installs only
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
			if seen[name] || registry.ValidateName(name) != nil {
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
			entry := pluginEntry{Name: name, Path: path, Target: target, Managed: dir == binDir}
			if entry.Managed {
				if m, err := LoadPluginManifest(name); err == nil {
					entry.Manifest = m
				}
			}
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
