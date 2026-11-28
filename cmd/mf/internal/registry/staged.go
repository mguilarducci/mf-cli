package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// InstallStaged installs a repository download: the executable at staged
// (consumed: moved into the registry) together with its manifest, under
// m.Name. It returns the registry entry it placed.
//
// Everything new is first built in a temporary package directory, so a
// refusal or a failure there leaves the previous Managed plugin untouched.
// Only then are the previous package and entries removed, the package moved
// into place, and the entry swapped in atomically, as InstallLocal does.
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
	if err := os.RemoveAll(pkgDir); err != nil {
		return "", fmt.Errorf("remove previous plugin package: %w", err)
	}
	if err := os.Rename(tmpPkg, pkgDir); err != nil {
		return "", fmt.Errorf("place plugin package: %w", err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return "", fmt.Errorf("create plugin dir: %w", err)
	}
	dst := filepath.Join(binDir, binName)
	if err := placeEntry(filepath.Join(pkgDir, binName), binDir, dst); err != nil {
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
	return dst, nil
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
