// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package registry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const binaryPrefix = "mf-"

// CanInstall reports whether name can be installed: it refuses an existing
// Managed plugin unless force, naming where it is installed. It never
// changes anything on disk.
func (r *Registry) CanInstall(name string, force bool) error {
	existing, err := r.entries(name)
	if err != nil {
		return err
	}
	if len(existing) > 0 && !force {
		return fmt.Errorf("plugin %q already installed at %s; use --force to replace", name, existing[0])
	}
	return nil
}

// entries returns the registry's bin entries for name that exist: one on
// Unix, any PATHEXT variant on Windows.
func (r *Registry) entries(name string) ([]string, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	dir, err := r.Dir()
	if err != nil {
		return nil, err
	}
	candidates := []string{binaryPrefix + name}
	if r.isWindows() {
		candidates = nil
		for _, ext := range r.pathExts() {
			candidates = append(candidates, binaryPrefix+name+ext)
		}
	}
	var out []string
	for _, c := range candidates {
		p := filepath.Join(dir, "bin", c)
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect plugin entry: %w", err)
		}
	}
	return out, nil
}

func (r *Registry) isWindows() bool { return r.env.GOOS == "windows" }

// pathExts is the lowercased PATHEXT list, defaulting like Windows does.
func (r *Registry) pathExts() []string {
	pathext := r.env.PathExt
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

// InstallLocal makes the executable at src runnable as `mf <name>` and
// returns the registry entry it placed. A symlink keeps a dev build live
// across rebuilds; a copy is the fallback for filesystems (or Windows
// accounts) that refuse symlinks. With force it replaces every existing
// variant of name and its old package.
//
// The new entry is built under a temporary name that never starts with mf-
// (so it is never listed or run), then renamed over the old one: a failed
// link or copy leaves the old plugin untouched and nothing partial behind.
func (r *Registry) InstallLocal(src, name string, force bool) (string, error) {
	if err := r.CanInstall(name, force); err != nil {
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
	if !r.isExecutable(abs, info) {
		return "", fmt.Errorf("%s is not an executable file", src)
	}
	existing, err := r.entries(name)
	if err != nil {
		return "", err
	}
	dir, err := r.Dir()
	if err != nil {
		return "", err
	}
	binDir := filepath.Join(dir, "bin")
	dst := filepath.Join(binDir, r.entryName(name, abs))
	pkgDir := filepath.Join(dir, "pkg", name)
	for _, p := range append(existing, dst) {
		if r.samePath(abs, p) {
			return "", fmt.Errorf("%s is already the registry entry for %q; install from the original file instead", src, name)
		}
	}
	if len(existing) > 0 && isWithin(abs, pkgDir) {
		return "", fmt.Errorf("%s is inside the package being replaced for %q; install from the original file instead", src, name)
	}
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}
	if err := placeEntry(abs, binDir, dst); err != nil {
		return "", err
	}
	for _, p := range existing {
		if p == dst {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove replaced plugin entry: %w", err)
		}
	}
	if len(existing) > 0 {
		if err := os.RemoveAll(pkgDir); err != nil {
			return "", fmt.Errorf("remove previous plugin package: %w", err)
		}
	}
	return dst, nil
}

// placeEntry links (or copies) target to a temporary name in binDir, then
// renames it to dst.
func placeEntry(target, binDir, dst string) error {
	tmpFile, err := os.CreateTemp(binDir, ".tmp-install-*")
	if err != nil {
		return fmt.Errorf("create plugin entry: %w", err)
	}
	tmp := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmp) }() // no-op once renamed
	if err := os.Remove(tmp); err != nil {
		return fmt.Errorf("create plugin entry: %w", err)
	}
	if err := os.Symlink(target, tmp); err != nil {
		if err := copyExecutable(target, tmp); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("place plugin entry: %w", err)
	}
	return nil
}

func copyExecutable(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // src is the path the user asked to install
	if err != nil {
		return fmt.Errorf("open plugin: %w", err)
	}
	defer func() { _ = in.Close() }()
	//nolint:gosec // dst is inside the registry's bin dir; plugins must be executable
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
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

// entryName is the file name a plugin gets in the bin dir. On Windows it
// keeps the source's extension (.exe, .bat, ...) so PATHEXT lookup finds it.
func (r *Registry) entryName(name, src string) string {
	if r.isWindows() {
		return binaryPrefix + name + strings.ToLower(filepath.Ext(src))
	}
	return binaryPrefix + name
}

// isExecutable reports whether path can be run as a plugin. Windows has no
// executable bit, so it goes by PATHEXT extension.
func (r *Registry) isExecutable(path string, info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	if r.isWindows() {
		ext := strings.ToLower(filepath.Ext(path))
		return ext != "" && slices.Contains(r.pathExts(), ext)
	}
	return info.Mode().Perm()&0o111 != 0
}

// samePath reports whether a and b name the same file path once cleaned.
// Windows paths compare case-insensitively.
func (r *Registry) samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || r.isWindows() && strings.EqualFold(a, b)
}

// isWithin reports whether path is inside dir.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
