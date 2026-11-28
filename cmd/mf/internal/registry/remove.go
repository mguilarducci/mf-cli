// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotManaged is returned when removing a name that is not a Managed plugin.
var ErrNotManaged = errors.New("not a managed plugin")

// Remove deletes the Managed plugin's entry (every Windows extension variant)
// and package. Its data dir is kept unless purge.
func (r *Registry) Remove(name string, purge bool) error {
	existing, err := r.entries(name)
	if err != nil {
		return err
	}
	dir, err := r.Dir()
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return fmt.Errorf("plugin %q is not installed in %s: %w", name, dir, ErrNotManaged)
	}
	for _, p := range existing {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove plugin: %w", err)
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, "pkg", name)); err != nil {
		return fmt.Errorf("remove plugin package: %w", err)
	}
	if purge {
		if err := os.RemoveAll(filepath.Join(dir, "data", name)); err != nil {
			return fmt.Errorf("remove plugin data: %w", err)
		}
	}
	return nil
}
