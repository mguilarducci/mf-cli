// Portions adapted from entireio/cli (https://github.com/entireio/cli),
// Copyright (c) 2026 Entire Inc., MIT License.
// See third_party/licenses/entireio-cli.LICENSE.

package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

const manifestFileName = "manifest.yml"

// Manifest records how a Managed plugin was installed from a repository.
// It lives at <dir>/pkg/<name>/manifest.yml; local installs have none.
type Manifest struct {
	Name        string    `json:"name"                  yaml:"name"`
	RepoURL     string    `json:"repo_url"              yaml:"repo_url"`
	Tag         string    `json:"tag"                   yaml:"tag"`
	Asset       string    `json:"asset,omitempty"       yaml:"asset,omitempty"`
	SHA256      string    `json:"sha256,omitempty"      yaml:"sha256,omitempty"`
	Unverified  bool      `json:"unverified,omitempty"  yaml:"unverified,omitempty"`
	Pinned      bool      `json:"pinned,omitempty"      yaml:"pinned,omitempty"`
	InstalledAt time.Time `json:"installed_at"          yaml:"installed_at"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
}

// loadManifest returns (nil, nil) when the plugin has no manifest.
func (r *Registry) loadManifest(name string) (*Manifest, error) {
	dir, err := r.Dir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "pkg", name, manifestFileName)) //nolint:gosec // inside the registry pkg dir, name validated
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // absence is the common case
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse plugin manifest: %w", err)
	}
	return &m, nil
}
