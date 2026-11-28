// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// InstallStaged installs a repository download: the executable at staged
// (consumed: moved into the registry) together with its manifest, under
// m.Name. It returns the registry entry it placed.
//
// Everything new is first built in a temporary package directory, so a
// refusal or a failure there leaves the previous Managed plugin untouched.
// Then each step keeps the plugin runnable: the package moves into place (an
// existing one has its binary and manifest replaced in place, each by an
// atomic rename, so an entry pointing into it never dangles), the entry is
// swapped in atomically as InstallLocal does, and only then is what the
// previous package held besides the new files removed.
func (r *Registry) InstallStaged(staged string, m *Manifest, force bool) (string, error) {
	if m == nil {
		return "", errors.New("install staged plugin: no manifest")
	}
	name := m.Name
	if err := r.CanInstall(name, force); err != nil {
		return "", err
	}
	info, err := os.Stat(staged)
	if err != nil {
		return "", fmt.Errorf("stat staged plugin: %w", err)
	}
	if !r.isExecutable(staged, info) {
		return "", fmt.Errorf("staged plugin %s is not an executable file", staged)
	}
	dir, err := r.Dir()
	if err != nil {
		return "", err
	}
	binName := r.entryName(name, staged)
	pkgRoot := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgRoot, 0o750); err != nil {
		return "", fmt.Errorf("create plugin pkg dir: %w", err)
	}
	// The temporary name starts with a dot, which no valid plugin name does.
	tmpPkg, err := os.MkdirTemp(pkgRoot, ".tmp-install-*")
	if err != nil {
		return "", fmt.Errorf("create plugin pkg dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpPkg) }() // no-op once renamed
	if err := moveExecutable(staged, filepath.Join(tmpPkg, binName)); err != nil {
		return "", err
	}
	if err := writeManifest(tmpPkg, m); err != nil {
		return "", err
	}

	// The new plugin is ready: replace the old one.
	existing, err := r.entries(name)
	if err != nil {
		return "", err
	}
	pkgDir := filepath.Join(pkgRoot, name)
	if err := placePackage(tmpPkg, pkgDir, binName); err != nil {
		return "", err
	}
	binDir := filepath.Join(dir, "bin")
	dst := filepath.Join(binDir, binName)
	if err := replaceEntry(filepath.Join(pkgDir, binName), binDir, dst, existing); err != nil {
		return "", err
	}
	if err := removeAllExcept(pkgDir, binName, manifestFileName); err != nil {
		return "", fmt.Errorf("remove previous plugin package: %w", err)
	}
	return dst, nil
}

// placePackage moves the package built in tmpPkg to pkgDir. When pkgDir
// already exists, its binary and manifest are replaced in place instead, so
// the path an existing entry points at always holds a runnable binary.
func placePackage(tmpPkg, pkgDir, binName string) error {
	info, err := os.Lstat(pkgDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(tmpPkg, pkgDir); err != nil {
			return fmt.Errorf("place plugin package: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect plugin package: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("place plugin package: %s is not a directory", pkgDir)
	}
	for _, f := range []string{binName, manifestFileName} {
		if err := os.Rename(filepath.Join(tmpPkg, f), filepath.Join(pkgDir, f)); err != nil {
			return fmt.Errorf("place plugin package: %w", err)
		}
	}
	return nil
}

// removeAllExcept removes everything in dir except the named entries.
func removeAllExcept(dir string, keep ...string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var firstErr error
	for _, e := range entries {
		if slices.Contains(keep, e.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// moveExecutable moves src to dst, copying when they are on different
// filesystems (staging usually lives under TMPDIR).
func moveExecutable(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyExecutable(src, dst); err != nil {
		return err
	}
	_ = os.Remove(src)
	return nil
}
